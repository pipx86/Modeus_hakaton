package sse_test

import (
	"context"
	"os"
	"testing"

	"activity-descriptor/internal/sse"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestИсторияСобытийPostgreSQL(t *testing.T) {
	url := os.Getenv("INGEST_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("нужна отдельная тестовая база PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var a, b string
	for _, id := range []*string{&a, &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('Тест изоляции SSE') RETURNING id::text`).Scan(id); err != nil {
			t.Fatal(err)
		}
		defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, *id)
	}
	store := sse.NewPGEventStore(pool)
	first := sse.NewEvent(a, sse.EventProgress, map[string]any{"n": 1})
	foreign := sse.NewEvent(b, sse.EventProgress, map[string]any{"n": 2})
	last := sse.NewEvent(a, sse.EventDone, map[string]any{"n": 3})
	for _, event := range []*sse.SSEEvent{first, foreign, last} {
		if err := store.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Append(first); err == nil {
		t.Fatal("повторный идентификатор события должен отклоняться")
	}
	// Курсор другого исследования не должен скрывать события выбранного исследования.
	page, err := store.GetRunAfter(a, foreign.ID, 1)
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("первая страница: %v, %v", page, err)
	}
	page, err = store.GetRunAfter(a, first.ID, 1)
	if err != nil || len(page) != 1 || page[0].ID != last.ID {
		t.Fatalf("вторая страница: %v, %v", page, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM run_sse_events WHERE run_id=$1`, a).Scan(&count); err != nil || count != 0 {
		t.Fatalf("каскадное удаление: %d, %v", count, err)
	}
	page, err = store.GetRunAfter(b, "", 10)
	if err != nil || len(page) != 1 || page[0].ID != foreign.ID {
		t.Fatalf("события другого исследования: %v, %v", page, err)
	}
}
