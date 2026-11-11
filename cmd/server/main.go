package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	if err = pool.QueryRow(connectCtx, "SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('runs') AND attname='stage' AND NOT attisdropped)").Scan(&schemaReady); err != nil || !schemaReady {
		return errors.New("база не подготовлена: примените основные миграции, включая 20261110000000_run_stage.sql")
	}

	listener, err := net.Listen("tcp", c.address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	pgStore := api.NewPGStore(pool)

	var chatClient llm.ChatClient = chat.UnavailableClient{}
	if cfg, cfgErr := agents.FromEnv(); cfgErr == nil {
		if agentPool, poolErr := agents.NewPool(cfg); poolErr == nil {
			if interviewerClient, clientErr := agentPool.Client(agents.Interviewer); clientErr == nil {
				chatClient = interviewerClient
			}
		}
	} else {
		log.Printf("LLM provider is not configured, interviewer fallback is used: %v", cfgErr)
	}
	chatService := chat.NewService(chatClient, func(runID string, status domain.RunStatus) {
		_ = pgStore.SetStatus(runID, status)
		switch status {
		case domain.RunDone:
			_ = pgStore.SetStage(runID, domain.StageDone)
		case domain.RunFailed:
			_ = pgStore.SetStage(runID, domain.StageFailed)
		}
	})

	server := &http.Server{Handler: api.NewServerWithChat(pgStore, chatService).Routes(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	st := store.New(pool)
	reader := store.NewVacancyAdapter(st)
	resumer := api.NewResumeProcessor(pgStore, pgStore, reader) // <-- api вместо store
	go func() {
		bgCtx := context.Background()
		if err := resumer.ProcessIncompleteRuns(bgCtx); err != nil {
			log.Printf("[startup] resume processor warning: %v", err)
		}
	}()

	log.Printf("development API listening on %s (PostgreSQL)", listener.Addr())
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
