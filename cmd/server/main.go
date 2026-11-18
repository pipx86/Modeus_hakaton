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
	"strings"
	"syscall"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
	agentmetrics "activity-descriptor/internal/llm/metrics"
	"activity-descriptor/internal/llmobs"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/metrics"
	"activity-descriptor/internal/observability"
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
	if err = pool.QueryRow(connectCtx, "SELECT (SELECT count(*)=2 FROM pg_attribute WHERE attrelid=to_regclass('runs') AND attname IN ('stage','type') AND NOT attisdropped) AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('evidence') AND attname='ref_id' AND NOT attisdropped) AND to_regclass('run_sse_events') IS NOT NULL").Scan(&schemaReady); err != nil || !schemaReady {
		return errors.New("база не подготовлена: примените основные миграции, включая 20261112000002_sse_events.sql")
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
	var llmProvider, llmModel string
	if cfg, cfgErr := agents.FromEnv(); cfgErr == nil {
		llmProvider = "openai-compatible"
		llmModel = cfg.Agents[agents.Interviewer].Model
		logging.Logger.Info("LLM provider configured",
			"base_url", cfg.BaseURL,
			"interviewer_model", cfg.Agents[agents.Interviewer].Model,
			"analyst_model", cfg.Agents[agents.Analyst].Model)
		if agentPool, poolErr := agents.NewPool(cfg); poolErr == nil {
			agentMetrics = agentPool.Metrics()
			if interviewerClient, clientErr := agentPool.Client(agents.Interviewer); clientErr == nil {
				chatClient = interviewerClient
			}
			if client, clientErr := agentPool.Client(agents.Analyst); clientErr == nil {
				analystClient = client
			}
		}
	} else {
		logging.Logger.Warn("LLM provider is not configured; using fallback interview", "error", cfgErr)
	}
	if agentMetrics != nil {
		provider := llmProvider
		model := llmModel
		chatClient = llmobs.NewRecorder(chatClient, llmStore, "interviewer", provider, model)
		if analystClient != nil {
			analystClient = llmobs.NewRecorder(analystClient, llmStore, "analyst", provider, model)
		}
		chatClient = llm.NewCachedClient(chatClient, 128)
		if analystClient != nil {
			analystClient = llm.NewCachedClient(analystClient, 128)
		}
	}
	interviewerVersion := activePromptVersion(ctx, llmStore, "interviewer")
	analystVersion := activePromptVersion(ctx, llmStore, "analyst")
	synthesizerVersion := activePromptVersion(ctx, llmStore, "synthesizer")
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
	if analystClient != nil {
		chatService.SetFinalizer(newTreeFinalizerWithVersions(pgStore, analystClient, analystVersion, synthesizerVersion, newRAGRetriever(pgStore)))
	}
	app = api.NewServerWithChat(pgStore, chatService)
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

	st := store.New(pool)
	reader := store.NewVacancyAdapter(st)
	resumer := api.NewResumeProcessor(pgStore, pgStore, reader)
	app.BindResumeProcessor(resumer)
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
