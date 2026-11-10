package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// RunStatusStore сохраняет только общий статус исследования.
// Для сохранения этапов оркестратора бэкенд использует api.PGStore.
type RunStatusStore struct {
	Store   *Store
	Timeout time.Duration
}

func (s RunStatusStore) Save(run *domain.Run) error {
	if s.Store == nil || run == nil || s.Timeout <= 0 {
		return errors.New("store, run and positive timeout are required")
	}
	var id pgtype.UUID
	if err := id.Scan(run.ID); err != nil || !id.Valid {
		return errors.New("run ID must be a valid UUID")
	}
	switch run.Status {
	case domain.RunPending, domain.RunRunning, domain.RunDone, domain.RunFailed:
	default:
		return errors.New("invalid run status")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
	defer cancel()
	n, err := s.Store.UpdateRunStatus(ctx, db.UpdateRunStatusParams{ID: id, Status: string(run.Status)})
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	if n != 1 {
		return errors.New("run not found")
	}
	return nil
}
