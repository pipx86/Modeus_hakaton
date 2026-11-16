package database_test

import (
	"activity-descriptor/internal/store"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestПоискСПереключениемНаBM25(t *testing.T) {
	url := os.Getenv("VECTOR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна VECTOR_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var run, source, fragment pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('поиск с заменой') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','источник') RETURNING id`, run).Scan(&source); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, run)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1`, source)
	}()
	if err = pool.QueryRow(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,0,'анализ данных') RETURNING id`, source).Scan(&fragment); err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, 768)
	vector[0] = 1
	literal := vectorLiteral(vector)
	if _, err = pool.Exec(ctx, `INSERT INTO fragment_embeddings(fragment_id,model,embedding) VALUES($1,'тест',$2::vector)`, fragment, literal); err != nil {
		t.Fatal(err)
	}
	success := func(context.Context, string) ([]float32, error) { return vector, nil }
	failed := func(context.Context, string) ([]float32, error) {
		return nil, errors.New("модель недоступна")
	}
	invalid := func(context.Context, string) ([]float32, error) { return []float32{1, 2}, nil }
	s := store.New(pool)
	for _, tc := range []struct {
		name     string
		provider store.EmbeddingFunc
		want     string
	}{{"вектор", success, "vector"}, {"ошибка модели", failed, "bm25"}, {"неверная размерность", invalid, "bm25"}, {"нет модели", nil, "bm25"}} {
		t.Run(tc.name, func(t *testing.T) {
			hits, mode, err := s.SearchFragments(ctx, run, "анализ данных", "тест", 5, tc.provider)
			if err != nil || mode != tc.want || len(hits) != 1 || hits[0].RefID != fragment || hits[0].SourceID != source {
				t.Fatalf("режим=%s результаты=%v ошибка=%v", mode, hits, err)
			}
		})
	}
	hybridHits, mode, err := s.SearchHybrid(ctx, run, "анализ данных", "тест", 5, success)
	if err != nil || mode != "hybrid" || len(hybridHits) != 1 || hybridHits[0].RefID != fragment {
		t.Fatalf("гибридный поиск: режим=%s результаты=%v ошибка=%v", mode, hybridHits, err)
	}
	fallbackHits, mode, err := s.SearchHybrid(ctx, run, "анализ данных", "тест", 5, failed)
	if err != nil || mode != "bm25" || len(fallbackHits) != 1 || fallbackHits[0].RefID != fragment {
		t.Fatalf("резервный гибридный поиск: режим=%s результаты=%v ошибка=%v", mode, fallbackHits, err)
	}
	if _, _, err := s.SearchHybrid(ctx, run, " ", "тест", 5, success); err == nil {
		t.Fatal("пустой запрос принят гибридным поиском")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.SearchFragments(cancelled, run, "анализ данных", "тест", 5, success); !errors.Is(err, context.Canceled) {
		t.Fatalf("отмена запроса потеряна: %v", err)
	}
}
