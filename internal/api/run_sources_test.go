package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAttachRunSourceAuthBoundary verifies the live attachment endpoint is
// mounted and protected: unauthenticated callers get 401, read-only roles 403,
// and a role allowed to create sources reaches the persistence layer (501 for
// the in-memory store, which cannot attach run sources).
func TestAttachRunSourceAuthBoundary(t *testing.T) {
	handler := NewServer(NewMemStoreForTest()).Routes()
	const path = "/api/v1/runs/00000000-0000-0000-0000-000000000000/sources"
	body := `{"type":"standard","title":"Профстандарт","content":"текст"}`

	if rec := do(t, handler, http.MethodPost, path, "", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	for _, role := range []string{"guest", "student"} {
		if rec := do(t, handler, http.MethodPost, path, role, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", role, rec.Code)
		}
	}
	if rec := do(t, handler, http.MethodPost, path, "teacher", body); rec.Code != http.StatusNotImplemented {
		t.Fatalf("teacher + in-memory store: status = %d, want 501", rec.Code)
	}
}

func runSourcesTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая PostgreSQL (INGEST_TEST_DATABASE_URL)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestAttachRunSourcePostgresEndToEnd covers the real HTTP live-flow path:
// a standard and a vacancy source are attached to a run through the API and are
// immediately visible as run-linked sources with their content.
func TestAttachRunSourcePostgresEndToEnd(t *testing.T) {
	pool := runSourcesTestPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	handler := NewServer(store).Routes()

	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	created := make([]string, 0, 2)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1::uuid`, run.ID)
		for _, id := range created {
			_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1::uuid`, id)
		}
	})

	standard := do(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/sources", "teacher",
		`{"type":"standard","title":"Профстандарт аналитика","content":"Трудовая задача: писать SQL-запросы."}`)
	if standard.Code != http.StatusCreated {
		t.Fatalf("attach standard = %d %s", standard.Code, standard.Body.String())
	}
	var standardSource domain.Source
	if err = json.Unmarshal(standard.Body.Bytes(), &standardSource); err != nil {
		t.Fatalf("standard JSON: %v (%s)", err, standard.Body.String())
	}
	created = append(created, standardSource.ID)
	if standardSource.Type != domain.SourceStandard || standardSource.Title != "Профстандарт аналитика" || standardSource.Content == "" {
		t.Fatalf("standard source = %+v", standardSource)
	}

	vacancy := do(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/sources", "admin",
		`{"type":"vacancy","title":"Вакансия Data Analyst","content":"Обязанности: строить дашборды."}`)
	if vacancy.Code != http.StatusCreated {
		t.Fatalf("attach vacancy = %d %s", vacancy.Code, vacancy.Body.String())
	}
	var vacancySource domain.Source
	if err = json.Unmarshal(vacancy.Body.Bytes(), &vacancySource); err != nil {
		t.Fatalf("vacancy JSON: %v", err)
	}
	created = append(created, vacancySource.ID)

	linked, err := store.ListRunSources(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListRunSources: %v", err)
	}
	types := make(map[domain.SourceType]bool)
	for _, source := range linked {
		types[source.Type] = true
	}
	if len(linked) != 2 || !types[domain.SourceStandard] || !types[domain.SourceVacancy] {
		t.Fatalf("linked sources = %+v, want standard + vacancy", linked)
	}

	withContent, err := store.ListRunSourceContents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListRunSourceContents: %v", err)
	}
	for _, source := range withContent {
		if strings.TrimSpace(source.Content) == "" {
			t.Fatalf("run source %+v has no content", source)
		}
	}
	if len(withContent) != 2 {
		t.Fatalf("run source contents = %d, want 2", len(withContent))
	}
}

// TestAttachRunSourceValidation covers the boundary errors of the endpoint.
func TestAttachRunSourceValidation(t *testing.T) {
	pool := runSourcesTestPool(t)
	store := NewPGStore(pool)
	handler := NewServer(store).Routes()

	run, err := store.CreateRun("Валидация")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1::uuid`, run.ID)
	})

	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{"invalid type", "/api/v1/runs/" + run.ID + "/sources",
			`{"type":"interview","title":"Интервью","content":"текст"}`, http.StatusUnprocessableEntity},
		{"empty content", "/api/v1/runs/" + run.ID + "/sources",
			`{"type":"standard","title":"Стандарт","content":"  "}`, http.StatusUnprocessableEntity},
		{"empty title", "/api/v1/runs/" + run.ID + "/sources",
			`{"type":"vacancy","title":"  ","content":"текст"}`, http.StatusUnprocessableEntity},
		{"bad json", "/api/v1/runs/" + run.ID + "/sources", `not-json`, http.StatusBadRequest},
		{"unknown run", "/api/v1/runs/00000000-0000-0000-0000-000000000000/sources",
			`{"type":"standard","title":"Стандарт","content":"текст"}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, handler, http.MethodPost, tc.path, "admin", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}

	// Malformed JSON must not have created any source.
	var count int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM sources WHERE run_id=$1::uuid`, run.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid requests created %d sources", count)
	}
}

// TestLinkRunSourcesSkipsMalformedIDs keeps the finalizer fallback best-effort.
func TestLinkRunSourcesSkipsMalformedIDs(t *testing.T) {
	pool := runSourcesTestPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	run, err := store.CreateRun("Связывание")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1::uuid`, run.ID)
	})
	if err = store.LinkRunSources(ctx, run.ID, []string{"", "not-a-uuid", "src-synthetic"}); err != nil {
		t.Fatalf("LinkRunSources with malformed ids = %v, want nil", err)
	}
	linked, err := store.ListRunSources(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 0 {
		t.Fatalf("linked = %+v, want none", linked)
	}
	if err = store.LinkRunSources(ctx, "not-a-uuid", []string{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LinkRunSources invalid run = %v, want ErrNotFound", err)
	}
}
