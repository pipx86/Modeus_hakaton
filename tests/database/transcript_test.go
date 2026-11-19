package database_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/search"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type transcriptTransport func(*http.Request) (*http.Response, error)

func (f transcriptTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTranscriptPostgresReadAndSearchRoundTrip(t *testing.T) {
	dsn := os.Getenv("VECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна PostgreSQL с пакетами поиска")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pg := api.NewPGStore(pool)
	run, err := pg.CreateRun("Специалист О'Нил <профиль> & 4")
	if err != nil {
		t.Fatal(err)
	}
	var source string
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM runs WHERE id=$1", run.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM sources WHERE id=$1", source)
	}()
	raw := "  Результат redpoint marker; О'Нил: <mark data-proof='literal'>& 3 < 7 \\ путь\n  "
	if err := pg.CreateInterviewSource(ctx, run.ID, source, raw); err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := pool.QueryRow(ctx, "SELECT content FROM sources WHERE id=$1", source).Scan(&saved); err != nil || saved != raw {
		t.Fatalf("SQL вернул %q, error=%v", saved, err)
	}
	var sourceID pgtype.UUID
	if err = sourceID.Scan(source); err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, 768)
	vector[0] = 1
	response, _ := json.Marshal(map[string]any{"embeddings": [][]float32{vector}})
	model := embeddings.Ollama{BaseURL: "http://test.invalid", Model: "fixture", Client: &http.Client{
		Transport: transcriptTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(response))}, nil
		}),
	}}
	if err := (search.Service{Pool: pool, Model: model}).IndexSource(ctx, sourceID); err != nil {
		t.Fatal(err)
	}
	auth.SetSecretForTests("test-only-token-secret-168434b-2")
	t.Setenv("OLLAMA_URL", "not-an-http-url")
	token, err := auth.Issue("read-test", auth.RoleTeacher, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := api.NewServer(pg).Routes()
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	sourceResp := get("/api/v1/sources/" + source)
	var item domain.Source
	if err := json.Unmarshal(sourceResp.Body.Bytes(), &item); sourceResp.Code != 200 || err != nil || item.Content != raw {
		t.Fatalf("source response %d %q %v", sourceResp.Code, item.Content, err)
	}
	endpoint := "/api/v1/runs/" + run.ID + "/search?"
	params := url.Values{"q": {"redpoint marker \"'<>;&"}}
	hits := get(endpoint + params.Encode())
	var result struct {
		Mode string
		Hits []store.SearchHit
	}
	if err := json.Unmarshal(hits.Body.Bytes(), &result); hits.Code != 200 || err != nil ||
		result.Mode != "bm25" || len(result.Hits) != 1 || result.Hits[0].Quote != raw {
		t.Fatalf("search response %d %q %v", hits.Code, hits.Body.String(), err)
	}
	params.Set("strategy", "custom order <example>")
	if rejected := get(endpoint + params.Encode()); rejected.Code != 400 {
		t.Fatalf("неизвестная стратегия вернула %d", rejected.Code)
	}
}
