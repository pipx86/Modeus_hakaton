package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log" // Оставляем для совместимости, но будем использовать logging.Logger
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
	agentmetrics "activity-descriptor/internal/llm/metrics"
	"activity-descriptor/internal/llmobs"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/metrics"
	"activity-descriptor/internal/observability"
	"activity-descriptor/internal/search"
	"activity-descriptor/internal/steps"
	"activity-descriptor/internal/store" // === NEW: для ResumeProcessor

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct{ address, databaseURL, jwtSecret string }

func loadConfig() (config, error) {
	c := config{address: os.Getenv("HTTP_ADDR"), databaseURL: os.Getenv("DATABASE_URL"), jwtSecret: os.Getenv("JWT_SECRET")}
	if c.address == "" {
		c.address = "127.0.0.1:8080"
	}
	if c.databaseURL == "" || strings.TrimSpace(c.jwtSecret) == "" {
		return config{}, errors.New("DATABASE_URL and JWT_SECRET are required")
	}
	return c, nil
}

// aiStepTimeout returns the per-attempt budget of an AI batch step. The
// default is one hour because analyst extraction can span many sources.
func aiStepTimeout() time.Duration {
	const defaultTimeout = time.Hour
	raw := strings.TrimSpace(os.Getenv("AI_STEP_TIMEOUT_S"))
	if raw == "" {
		return defaultTimeout
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

// sourceAutoIndexEnabled reports whether new sources should be indexed in the
// background. Enabled by default; SOURCE_AUTO_INDEX=false disables the
// background embedding calls (for example on a server without Ollama).
func sourceAutoIndexEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("SOURCE_AUTO_INDEX"))
	if raw == "" {
		return true
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		logging.Logger.Warn("source_auto_index_setting_invalid", "value", raw, "fallback", true)
		return true
	}
	return enabled
}

func run(ctx context.Context, c config) error {
	logging.Init()
	shutdownTracing, err := observability.Setup()
	if err != nil {
		logging.Logger.Warn("tracing_setup_failed", "error", err)
	} else {
		if endpoint := observability.ConfigFromEnv().Endpoint; endpoint != "" {
			logging.Logger.Info("tracing_export_enabled", "exporter", "otlp-http", "endpoint", endpoint)
		} else {
			logging.Logger.Info("tracing_export_disabled", "reason", "OTEL_EXPORTER_OTLP_ENDPOINT is not set; spans stay local")
		}
		defer func() { _ = shutdownTracing(context.Background()) }()
	} // === NEW: Инициализация логгера ===

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(connectCtx, c.databaseURL)
	if err != nil {
		return errors.New("invalid PostgreSQL configuration")
	}
	defer pool.Close()
	if err = pool.Ping(connectCtx); err != nil {
		return errors.New("cannot connect to PostgreSQL")
	}
	var schemaReady bool
	if err = pool.QueryRow(connectCtx, "SELECT (SELECT count(*)=2 FROM pg_attribute WHERE attrelid=to_regclass('runs') AND attname IN ('stage','type') AND NOT attisdropped) AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('evidence') AND attname='ref_id' AND NOT attisdropped) AND to_regclass('run_sse_events') IS NOT NULL AND to_regclass('run_chat_messages') IS NOT NULL AND to_regclass('run_chat_sessions') IS NOT NULL").Scan(&schemaReady); err != nil || !schemaReady {
		return errors.New("база не подготовлена: примените основные миграции, включая 20261112000002_sse_events.sql, 20261118030000_run_chat_messages.sql и 20261119010000_run_chat_sessions.sql")
	}

	listener, err := net.Listen("tcp", c.address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	metricsRegistry := metrics.New()
	llmStore := llmobs.NewStore(pool)
	pgStore := api.NewPGStore(pool)
	var vacancyCount int
	if err := pool.QueryRow(connectCtx, "SELECT count(*) FROM sources WHERE type='vacancy'").Scan(&vacancyCount); err == nil {
		metricsRegistry.SetVacancies(vacancyCount)
	}
	var app *api.Server
	var agentMetrics *agentmetrics.Registry
	var chatClient llm.ChatClient = chat.UnavailableClient{}
	var analystClient llm.ChatClient
	if cfg, cfgErr := agents.FromEnv(); cfgErr == nil {
		logging.Logger.Info("LLM provider configured",
			"base_url", cfg.BaseURL,
			"interviewer_model", cfg.Agents[agents.Interviewer].Model,
			"interviewer_fallback_model", cfg.Agents[agents.Interviewer].FallbackModel,
			"analyst_model", cfg.Agents[agents.Analyst].Model,
			"analyst_fallback_model", cfg.Agents[agents.Analyst].FallbackModel)
		// The pool wraps every per-model client with this decorator, so the
		// llm_requests audit row names the model that actually answered. A
		// fallback call therefore shows up as a second row with its own model
		// instead of being attributed to the primary model.
		recordLLMRequest := func(name agents.AgentName, model string, client llm.ChatClient) llm.ChatClient {
			return llmobs.NewRecorder(client, llmStore, string(name), "openai-compatible", model)
		}
		if agentPool, poolErr := agents.NewPool(cfg, agents.WithClientDecorator(recordLLMRequest)); poolErr == nil {
			agentMetrics = agentPool.Metrics()
			if interviewerClient, clientErr := agentPool.Client(agents.Interviewer); clientErr == nil {
				chatClient = interviewerClient
			}
			if client, clientErr := agentPool.Client(agents.Analyst); clientErr == nil {
				analystClient = client
			}
		} else {
			logging.Logger.Error("LLM provider pool setup failed; using fallback interview", "error", poolErr)
		}
	} else {
		logging.Logger.Warn("LLM provider is not configured; using fallback interview", "error", cfgErr)
	}
	if agentMetrics != nil {
		chatClient = llm.NewCachedClient(chatClient, 128)
		if analystClient != nil {
			analystClient = llm.NewCachedClient(analystClient, 128)
		}
	}
	interviewerVersion := activePromptVersion(ctx, llmStore, "interviewer")
	analystVersion := activePromptVersion(ctx, llmStore, "analyst")
	synthesizerVersion := activePromptVersion(ctx, llmStore, "synthesizer")
	// The batch orchestrator (domain.Orchestrate) runs the same analyst and
	// synthesizer agents as the chat finalizer. The plan is built once here and
	// handed to both the create-run handler and the resume processor.
	var batchSynth steps.Synthesizer
	if analystClient != nil {
		if synthAgent, synthErr := synthesizer.New(synthesizer.WithPromptVersion(synthesizerVersion)); synthErr != nil {
			logging.Logger.Error("batch synthesizer setup failed; batch runs keep the vacancy pipeline", "error", synthErr)
		} else {
			batchSynth = synthAgent
		}
	}
	batchPlan := steps.NewBatchPlan(steps.BatchOptions{
		Analyst:              analystClient,
		AnalystPromptVersion: analystVersion,
		Synthesizer:          batchSynth,
		Store:                pgStore,
		VacancyTopK:          10,
		AITimeout:            aiStepTimeout(),
		ZunEnricher: func(ctx context.Context, profession string, nodes []domain.Node) []domain.Node {
			return enrichZun(ctx, analystClient, profession, nodes)
		},
	})
	logging.Logger.Info("batch pipeline configured",
		"ai", batchPlan.HasAI(),
		"analyst_prompt_version", analystVersion,
		"synthesizer_prompt_version", synthesizerVersion,
		"ai_step_timeout", batchPlan.AITimeout,
		"vacancy_step_timeout", batchPlan.FallbackTimeout)
	st := store.New(pool)
	vacancyReader := store.NewVacancyAdapter(st)
	chatService := chat.NewService(chatClient, func(runID string, status domain.RunStatus) {
		metricsRegistry.ObserveRun(string(status), 0)
		if err := pgStore.SetStatus(runID, status); err != nil {
			logging.Logger.Error("chat_status_store_failed", "run_id", runID, "error", err)
			return
		}
		if app != nil {
			if err := app.AppendRunStatusEvent(runID, status); err != nil {
				logging.Logger.Error("chat_status_event_failed", "run_id", runID, "error", err)
			}
		}
	})
	chatService.SetPromptVersion(interviewerVersion)
	chatService.SetSourceContextLoader(newInterviewSourceContext(pgStore, vacancyReader))
	// Persist the dialog and the interviewer session cursor, so chat history
	// survives restarts and an active interview resumes instead of hanging.
	// ChatSessionStore implements both chat.SessionStore and chat.MessageStore.
	chatService.SetSessionStore(store.NewChatSessionStore(pool))
	if analystClient != nil {
		chatService.SetFinalizer(newTreeFinalizerWithVersions(pgStore, analystClient, analystVersion, synthesizerVersion, newRAGRetriever(pgStore)))
	}
	app = api.NewServerWithChat(pgStore, chatService)
	app.SetBatchPlan(batchPlan)
	startHHVacancyImporter(context.Background(), pool)
	// Семантическая индексация: источники, созданные через POST /sources и
	// POST /sources/upload, индексируются в фоне (фрагменты + векторы Ollama).
	// Уже существующие источники догоняет cmd/index-sources (README.md).
	if sourceAutoIndexEnabled() {
		if app.EnableSourceAutoIndex() {
			embedConfig := search.ConfigFromEnv()
			logging.Logger.Info("source_auto_index_enabled",
				"ollama_url", embedConfig.OllamaURL,
				"embed_model", embedConfig.EmbedModel)
		} else {
			logging.Logger.Warn("source_auto_index_unavailable", "reason", "PostgreSQL store is required")
		}
	} else {
		logging.Logger.Info("source_auto_index_disabled", "reason", "SOURCE_AUTO_INDEX=false")
	}
	app.SetImportFiles(ingestion.ImportFiles{
		Vacancies:      os.Getenv("INGEST_VACANCIES_FILE"),
		VacancyFormat:  os.Getenv("INGEST_VACANCIES_FORMAT"),
		Standard:       os.Getenv("INGEST_STANDARD_FILE"),
		StandardFormat: os.Getenv("INGEST_STANDARD_FORMAT"),
		StandardKey:    os.Getenv("INGEST_STANDARD_KEY"),
		StandardTitle:  os.Getenv("INGEST_STANDARD_TITLE"),
	})
	rawHandler := app.Routes()
	rootMux := http.NewServeMux()
	rootMux.Handle("/api/v1/llm/", auth.Middleware(llmobs.NewHandler(llmobs.NewStore(pool))))
	rootMux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, metricsRegistry.Text())
		if agentMetrics != nil {
			_ = agentMetrics.WritePrometheus(w)
		}
	})
	rootMux.Handle("/", rawHandler)
	handler := logging.RequestLogger(metricsRegistry.Middleware(rootMux)) // === NEW: Оборачиваем в логгер ===

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	resumer := api.NewResumeProcessor(pgStore, pgStore, vacancyReader)
	resumer.SetBatchPlan(batchPlan)
	app.BindResumeProcessor(resumer)
	// A restart may have interrupted a chat run between two durable steps:
	// resume finalizing sessions, reconcile run statuses and mark legacy chat
	// runs that cannot be restored as failed (SESSION_INTERRUPTED).
	go chatService.RecoverSessions(context.Background())
	go func() {
		bgCtx := context.Background()
		if err := resumer.ProcessIncompleteRuns(bgCtx); err != nil {
			log.Printf("[startup] resume processor warning: %v", err)
		}
	}()

	logging.Logger.Info("development API listening", "addr", listener.Addr(), "db", "postgresql") // === NEW: slog вместо log.Printf ===
	select {
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}
}

// @title           Activity Descriptor API
// @version         1.0
// @description     Backend service for extracting competency trees from job descriptions and interviews.
// @BasePath        /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		log.Fatal(err)
	}
}

func activePromptVersion(ctx context.Context, store *llmobs.Store, agent string) string {
	if store == nil {
		return ""
	}
	version, err := store.ActivePromptVersion(ctx, agent)
	if err != nil {
		return ""
	}
	return version.Version
}
