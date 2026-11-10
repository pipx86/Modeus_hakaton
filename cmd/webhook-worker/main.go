// Команда webhook-worker читает события PostgreSQL и отправляет их подписанными получателю.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"activity-descriptor/internal/outbox"
	"activity-descriptor/internal/webhook"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	databaseURL, endpoint, secret := os.Getenv("DATABASE_URL"), os.Getenv("WEBHOOK_URL"), os.Getenv("WEBHOOK_SECRET")
	if databaseURL == "" || endpoint == "" || secret == "" {
		return errors.New("DATABASE_URL, WEBHOOK_URL and WEBHOOK_SECRET are required")
	}
	sender, err := webhook.NewSender(endpoint, []byte(secret))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("could not connect to database")
	}
	worker := outbox.Worker{Pool: pool, Sender: sender, RetryDelay: time.Minute}
	for {
		found, err := worker.DeliverOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("event delivery failed: %v", err)
		}
		if found && err == nil {
			continue
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
