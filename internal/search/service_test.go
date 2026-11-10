package search

import (
	"activity-descriptor/internal/embeddings"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func searchFixture(t *testing.T) (*pgxpool.Pool, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна тестовая PostgreSQL")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var run, source pgtype.UUID
	if err = p.QueryRow(ctx, "INSERT INTO runs(profession) VALUES('проверка поиска') RETURNING id").Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, "INSERT INTO sources(run_id,type,title,content) VALUES($1,'document','проверка поиска','Анализ данных и проверка качества данных') RETURNING id", run).Scan(&source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Exec(ctx, "DELETE FROM runs WHERE id=$1", run)
		p.Exec(ctx, "DELETE FROM sources WHERE id=$1", source)
	})
	return p, run, source
}
func TestИндексированиеИЗапаснойПоиск(t *testing.T) {
	p, run, source := searchFixture(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := make([]float32, 768)
		v[0] = 1
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{v}})
	}))
	defer server.Close()
	s := Service{Pool: p, Model: embeddings.Ollama{BaseURL: server.URL, Model: "тест", Client: server.Client()}}
	if err := s.IndexSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	hits, mode, err := s.Search(ctx, run, "анализ данных", 5)
	if err != nil || mode != "vector" || len(hits) != 1 {
		t.Fatalf("%s %v %v", mode, hits, err)
	}
	hybrid, hybridMode, hybridErr := s.HybridSearch(ctx, run, "анализ данных", 5)
	if hybridErr != nil || hybridMode != "hybrid" || len(hybrid) != 1 {
		t.Fatalf("%s %v %v", hybridMode, hybrid, hybridErr)
	}
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer unavailable.Close()
	s.Model.BaseURL = unavailable.URL
	s.Model.Client = unavailable.Client()
	if err = s.IndexSource(ctx, source); err == nil {
		t.Fatal("ошибка модели не замечена")
	}
	hits, mode, err = s.Search(ctx, run, "анализ данных", 5)
	if err != nil || mode != "bm25" || len(hits) != 1 {
		t.Fatalf("%s %v %v", mode, hits, err)
	}
	var count int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM fragment_embeddings e JOIN source_fragments f ON f.id=e.fragment_id WHERE f.source_id=$1", source).Scan(&count); err != nil || count != 1 {
		t.Fatalf("прежний индекс потерян: %d %v", count, err)
	}
}
func TestНастоящаяOllamaДесятьЗапросов(t *testing.T) {
	address := os.Getenv("OLLAMA_TEST_URL")
	if address == "" {
		t.Skip("нужна настоящая Ollama в OLLAMA_TEST_URL")
	}
	p, run, source := searchFixture(t)
	ctx := context.Background()
	model := os.Getenv("OLLAMA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	s := Service{Pool: p, Model: embeddings.Ollama{BaseURL: address, Model: model}}
	if err := s.IndexSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	// Предварительно загружаем модель; отдельно измеряем десять запросов тёплого сервиса.
	if _, _, err := s.Search(ctx, run, "анализ", 5); err != nil {
		t.Fatal(err)
	}
	var maximum time.Duration
	for i := 0; i < 10; i++ {
		start := time.Now()
		hits, mode, err := s.Search(ctx, run, "анализ данных", 5)
		elapsed := time.Since(start)
		if err != nil || mode != "vector" || len(hits) == 0 {
			t.Fatalf("%s %v %v", mode, hits, err)
		}
		if elapsed > maximum {
			maximum = elapsed
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("запрос %d: %s >= 2 секунды", i+1, elapsed)
		}
	}
	t.Logf("10 запросов с настоящей моделью, максимальная задержка: %s; тестовый корпус: один фрагмент", maximum)
}
