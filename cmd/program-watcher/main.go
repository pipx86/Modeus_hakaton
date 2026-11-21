// program-watcher проверяет зарегистрированные URL образовательных программ и
// создаёт черновики импорта при изменении документа.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/programwatch"

	"github.com/jackc/pgx/v5/pgxpool"
)

const sourcesPerCycle = 5

func main() {
	if err := run(); err != nil {
		logging.L().Error("program_watcher_failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logging.Init()
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "PostgreSQL DSN (default: DATABASE_URL)")
	pollInterval := flag.Duration("interval", 15*time.Minute, "how often to look for due sources")
	once := flag.Bool("once", false, "process one cycle, then exit")
	flag.Parse()

	if *dsn == "" {
		return errors.New("нужен -dsn или DATABASE_URL")
	}
	if *pollInterval <= 0 {
		return errors.New("-interval должен быть положительным")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return errors.New("некорректная конфигурация PostgreSQL")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("PostgreSQL недоступен: %w", err)
	}

	repository := programwatch.NewRepository(pool)
	// PGStore реализует programwatch.DraftCreator и создаёт черновики тем же
	// SQL, что и POST /admin/programs/import.
	checker := programwatch.NewChecker(repository, api.NewPGStore(pool))
	checker.Logger = logging.L()

	if *once {
		if _, err = checker.RunOnceAndLog(ctx, sourcesPerCycle); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	}

	for {
		if _, cycleErr := checker.RunOnceAndLog(ctx, sourcesPerCycle); cycleErr != nil && ctx.Err() == nil {
			logging.L().Error("program_watcher_cycle_failed", "error", cycleErr)
		}
		timer := time.NewTimer(*pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			logging.L().Info("program_watcher_stopped")
			return nil
		case <-timer.C:
		}
	}
}
