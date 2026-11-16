package main

import (
	"context"
	"errors"
	"fmt"
	"log" // Оставляем для совместимости, но будем использовать logging.Logger
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
	"activity-descriptor/internal/logging"
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
	logging.Init() // === NEW: Инициализация логгера ===

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

	pgStore := api.NewPGStore(pool)
	var app *api.Server
	var chatClient llm.ChatClient = chat.UnavailableClient{}
	var analystClient llm.ChatClient
	if cfg, cfgErr := agents.FromEnv(); cfgErr == nil {
		if agentPool, poolErr := agents.NewPool(cfg); poolErr == nil {
			if interviewerClient, clientErr := agentPool.Client(agents.Interviewer); clientErr == nil {
				chatClient = interviewerClient
			}
			if client, clientErr := agentPool.Client(agents.Analyst); clientErr == nil {
				analystClient = client
			}
		}
	} else {
		logging.Logger.Info("LLM provider is not configured; using fallback interview")
	}
	chatService := chat.NewService(chatClient, func(runID string, status domain.RunStatus) {
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
	if analystClient != nil {
		chatService.SetFinalizer(newTreeFinalizer(pgStore, analystClient))
	}
	app = api.NewServerWithChat(pgStore, chatService)
	rawHandler := app.Routes()
	handler := logging.RequestLogger(rawHandler) // === NEW: Оборачиваем в логгер ===

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
