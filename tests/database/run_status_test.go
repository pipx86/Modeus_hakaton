package database_test

import (
	"activity-descriptor/internal/store"
	"context"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunStatusCreatesCompletionEvent(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set STORE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO runs (profession) VALUES ('status test') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1::uuid`, id)
	adapter := store.RunStatusStore{Store: store.New(pool), Timeout: 5 * time.Second}
	run := &domain.Run{ID: id, Profession: "status test", Status: domain.RunRunning}
	if err := adapter.Save(run); err != nil {
		t.Fatal(err)
	}
	run.Status = domain.RunDone
	if err := adapter.Save(run); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Save(run); err != nil {
		t.Fatal(err)
	}
	var status string
	var count int
	if err := pool.QueryRow(ctx, `SELECT status, (SELECT count(*) FROM agent_messages WHERE run_id=$1::uuid AND message_type='run.completed') FROM runs WHERE id=$1::uuid`, id).Scan(&status, &count); err != nil {
		t.Fatal(err)
	}
	if status != "done" || count != 1 {
		t.Fatalf("status=%s events=%d", status, count)
	}
	run.ID = "00000000-0000-4000-8000-000000000000"
	if err := adapter.Save(run); err == nil {
		t.Fatal("expected missing run error")
	}
}
