package api

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"activity-descriptor/internal/sse"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresSSEВосстановлениеЧерезHTTP(t *testing.T) {
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
	var runID string
	if err := pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('Тест SSE API') RETURNING id::text`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, runID)
	store := sse.NewPGEventStore(pool)
	first := sse.NewEvent(runID, sse.EventProgress, map[string]any{"n": 1})
	last := sse.NewEvent(runID, sse.EventDone, map[string]any{"n": 2})
	if err := store.Append(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(last); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.SetPathValue("id", runID)
	request.Header.Set("Last-Event-ID", first.ID)
	response := httptest.NewRecorder()
	NewServer(NewPGStore(pool)).handleSSE(response, request)
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, "id: "+last.ID) || strings.Contains(body, "id: "+first.ID) {
		t.Fatalf("код %d, тело %q", response.Code, body)
	}
}
