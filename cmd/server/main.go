// Команда server запускает API с постоянным хранилищем PostgreSQL.
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
	listener, err := net.Listen("tcp", c.address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{Handler: api.NewServer(api.NewPGStore(pool)).Routes(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
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
