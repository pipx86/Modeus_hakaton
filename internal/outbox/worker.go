// Package outbox delivers persisted run.completed events at least once.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Sender interface {
	Send(context.Context, []byte) error
}

type Worker struct {
	Pool       *pgxpool.Pool
	Sender     Sender
	RetryDelay time.Duration
}

// DeliverOne locks one due event while sending. A crash can cause redelivery,
// so the receiver must deduplicate using run_id and event type.
// found is false when no due events exist. Delivery errors are returned after
// recording the failure and scheduling the next attempt.
func (w *Worker) DeliverOne(ctx context.Context) (found bool, err error) {
	if w.Pool == nil || w.Sender == nil || w.RetryDelay <= 0 {
		return false, errors.New("pool, sender, and positive retry delay are required")
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin delivery: %w", err)
	}
	defer tx.Rollback(context.Background())
	var id string
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT id::text, payload FROM agent_messages
 WHERE message_type = 'run.completed' AND status IN ('pending','failed') AND available_at <= now()
 ORDER BY available_at, created_at, id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select event: %w", err)
	}
	sendErr := w.Sender.Send(ctx, payload)
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if sendErr != nil {
		_, err = tx.Exec(ctx, `UPDATE agent_messages SET status='failed', attempts=attempts+1, available_at=now()+($2::bigint * interval '1 microsecond') WHERE id=$1::uuid`, id, w.RetryDelay.Microseconds())
	} else {
		_, err = tx.Exec(ctx, `UPDATE agent_messages SET status='sent', attempts=attempts+1, sent_at=now() WHERE id=$1::uuid`, id)
	}
	if err != nil {
		return true, fmt.Errorf("update event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit delivery: %w", err)
	}
	if sendErr != nil {
		return true, fmt.Errorf("send event: %w", sendErr)
	}
	return true, nil
}
