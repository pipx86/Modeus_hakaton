package sse

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type eventDatabase interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Begin(context.Context) (pgx.Tx, error)
}

// PGEventStore хранит историю событий в той же базе, что и исследования.
type PGEventStore struct{ pool eventDatabase }

func NewPGEventStore(pool eventDatabase) *PGEventStore { return &PGEventStore{pool: pool} }

func (s *PGEventStore) Append(event *SSEEvent) error {
	data, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Порядок выдачи номеров должен совпадать с порядком фиксации транзакций.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1836016755, 1936942437)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO run_sse_events(event_id,run_id,event_type,payload,occurred_at) VALUES($1,$2,$3,$4,$5)`, event.ID, event.RunID, event.Type, data, event.Timestamp); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PGEventStore) GetAfter(lastEventID string, limit int) ([]*SSEEvent, error) {
	return s.getAfter("", lastEventID, limit)
}

// GetRunAfter исключает события других исследований до применения ограничения размера страницы.
func (s *PGEventStore) GetRunAfter(runID, lastEventID string, limit int) ([]*SSEEvent, error) {
	return s.getAfter(runID, lastEventID, limit)
}

func (s *PGEventStore) getAfter(runID, lastEventID string, limit int) ([]*SSEEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT event_id,run_id::text,event_type,payload,occurred_at FROM run_sse_events
        WHERE ($1 = '' OR run_id::text = $1)
          AND sequence_id > COALESCE((SELECT sequence_id FROM run_sse_events WHERE event_id=$2 AND ($1 = '' OR run_id::text = $1)),0)
        ORDER BY sequence_id LIMIT $3`, runID, lastEventID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*SSEEvent, 0)
	for rows.Next() {
		e := &SSEEvent{}
		var payload []byte
		if err := rows.Scan(&e.ID, &e.RunID, &e.Type, &payload, &e.Timestamp); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Доставку новых событий обработчик проверяет опросом базы: это работает и для нескольких процессов.
func (s *PGEventStore) SubscribeLive(func(*SSEEvent)) func() { return func() {} }
func (s *PGEventStore) Close() error                         { return nil }
