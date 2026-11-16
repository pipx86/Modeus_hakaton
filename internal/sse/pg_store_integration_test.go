package sse

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGEventStoreИсторияПослеПерезапуска(t *testing.T) {
	url := os.Getenv("INGEST_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("нужна отдельная тестовая БД")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var a, b string
	for _, id := range []*string{&a, &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('Тест SSE') RETURNING id::text`).Scan(id); err != nil {
			t.Fatal(err)
		}
		defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, *id)
	}
	first := NewEvent(a, EventStageChange, map[string]any{"stage": "analysis"})
	other := NewEvent(b, EventProgress, map[string]any{"value": 1})
	last := NewEvent(a, EventDone, map[string]any{"ok": true})
	store := NewPGEventStore(pool)
	for _, e := range []*SSEEvent{first, other, last} {
		if err := store.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	// Новый экземпляр хранилища имитирует перезапуск процесса.
	restarted := NewPGEventStore(pool)
	page, err := restarted.GetRunAfter(a, "", 1)
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("первая страница: %v, %v", page, err)
	}
	page, err = restarted.GetRunAfter(a, first.ID, 10)
	if err != nil || len(page) != 1 || page[0].ID != last.ID {
		t.Fatalf("продолжение: %v, %v", page, err)
	}
	page, err = restarted.GetRunAfter(a, last.ID, 10)
	if err != nil || len(page) != 0 {
		t.Fatalf("после последнего события: %v, %v", page, err)
	}
}
