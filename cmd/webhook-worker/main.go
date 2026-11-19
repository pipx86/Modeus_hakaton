// webhook-worker polls PostgreSQL for run.completed events and sends them to n8n.
package main

import (
	"context"
	"errors"
	"flag"
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
	once := flag.Bool("once", false, "deliver every due event, then exit")
	pollInterval := flag.Duration("poll-interval", 2*time.Second, "idle polling interval")
	retryDelay := flag.Duration("retry-delay", time.Minute, "delay before a failed event is retried")
	flag.Parse()
	if *pollInterval <= 0 || *retryDelay <= 0 {
		return errors.New("poll-interval and retry-delay must be positive")
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
	worker := outbox.Worker{Pool: pool, Sender: sender, RetryDelay: *retryDelay}
	failed := false
	for {
		found, err := worker.DeliverOne(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("event delivery failed: %v", err)
			failed = true
		}
		if found && err == nil {
			log.Printf("run.completed event delivered")
			continue
		}
		if *once {
			break
		}
		timer := time.NewTimer(*pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	if failed {
		return errors.New("one or more events could not be delivered")
	}
	return nil
}
