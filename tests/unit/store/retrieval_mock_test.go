package store_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
)

func searchRows(hits ...store.SearchHit) *pgxmock.Rows {
	rows := pgxmock.NewRows([]string{"ref_id", "source_id", "quote", "score"})
	for _, hit := range hits {
		rows.AddRow(hit.RefID, hit.SourceID, hit.Quote, hit.Score)
	}
	return rows
}

func searchHit(ref, source int, quote string, score float64) store.SearchHit {
	return store.SearchHit{RefID: testUUID(ref), SourceID: testUUID(source), Quote: quote, Score: score}
}

func vectorEmbedding(context.Context, string) ([]float32, error) {
	vector := make([]float32, 768)
	vector[0] = 0.5
	return vector, nil
}

func TestSearchFragmentsUnit(t *testing.T) {
	validRun := testUUID(500)
	t.Run("проверяет обязательные данные", func(t *testing.T) {
		var nilStore *store.Store
		if _, _, err := nilStore.SearchFragments(context.Background(), validRun, "текст", "", 5, nil); err == nil {
			t.Fatal("ожидалось отсутствие подключения")
		}
		if _, _, err := store.New(nil).SearchFragments(context.Background(), validRun, "текст", "", 5, nil); err == nil {
			t.Fatal("ожидалось отсутствие подключения")
		}
		pool, s := testStore(t)
		for _, input := range []struct {
			run   pgtype.UUID
			query string
			limit int
		}{
			{pgtype.UUID{}, "текст", 5},
			{validRun, "  ", 5},
			{validRun, "текст", 0},
			{validRun, "текст", 101},
		} {
			if _, _, err := s.SearchFragments(context.Background(), input.run, input.query, "", input.limit, nil); err == nil {
				t.Fatalf("ожидалась ошибка для %+v", input)
			}
		}
		if _, _, err := s.SearchFragments(context.Background(), validRun, "текст", "", 5, vectorEmbedding); err == nil {
			t.Fatal("для векторного поиска требуется имя модели")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("отменённый запрос не обращается к базе", func(t *testing.T) {
		pool, s := testStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := s.SearchFragments(ctx, validRun, "текст", "", 5, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена контекста, получено %v", err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("лексический поиск возвращает найденное", func(t *testing.T) {
		pool, s := testStore(t)
		hits := []store.SearchHit{searchHit(1, 11, "цитата", 0.7)}
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(searchRows(hits...))
		got, mode, err := s.SearchFragments(context.Background(), validRun, "запрос", "", 5, nil)
		if err != nil || mode != "bm25" || len(got) != 1 || got[0].Quote != "цитата" {
			t.Fatalf("неверный ответ: %v %s %+v", err, mode, got)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("успешно использует вектор", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_relevant_fragments").WithArgs(anyArgs(4)...).WillReturnRows(searchRows(searchHit(1, 11, "векторная цитата", 0.9)))
		got, mode, err := s.SearchFragments(context.Background(), validRun, "запрос", "модель", 5, vectorEmbedding)
		if err != nil || mode != "vector" || len(got) != 1 || got[0].Quote != "векторная цитата" {
			t.Fatalf("неверный ответ: %v %s %+v", err, mode, got)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("при сбое эмбеддинга переключается на BM25", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(searchRows())
		badEmbed := func(context.Context, string) ([]float32, error) {
			return nil, errors.New("модель недоступна")
		}
		if _, mode, err := s.SearchFragments(context.Background(), validRun, "запрос", "модель", 5, badEmbed); err != nil || mode != "bm25" {
			t.Fatalf("не сработал резервный поиск: %v, %s", err, mode)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("некорректный вектор включает резервный поиск", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(searchRows())
		badEmbed := func(context.Context, string) ([]float32, error) { return []float32{1, 2}, nil }
		if _, mode, err := s.SearchFragments(context.Background(), validRun, "запрос", "модель", 5, badEmbed); err != nil || mode != "bm25" {
			t.Fatalf("не сработал резервный поиск: %v, %s", err, mode)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("эмбеддинг может отменить запрос", func(t *testing.T) {
		pool, s := testStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		embed := func(context.Context, string) ([]float32, error) { cancel(); return nil, errors.New("сбой") }
		if _, _, err := s.SearchFragments(ctx, validRun, "запрос", "модель", 5, embed); !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена контекста, получено %v", err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка запроса базы возвращается", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnError(errors.New("сбой базы"))
		if _, mode, err := s.SearchFragments(context.Background(), validRun, "запрос", "", 5, nil); err == nil || mode != "bm25" {
			t.Fatalf("ожидалась ошибка BM25: %v, %s", err, mode)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка сканирования результата возвращается", func(t *testing.T) {
		pool, s := testStore(t)
		rows := pgxmock.NewRows([]string{"ref_id", "source_id", "quote", "score"}).AddRow(testUUID(1), testUUID(2), "цитата", "не число")
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(rows)
		if _, _, err := s.SearchFragments(context.Background(), validRun, "запрос", "", 5, nil); err == nil {
			t.Fatal("ожидалась ошибка сканирования")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка чтения строк возвращается", func(t *testing.T) {
		pool, s := testStore(t)
		rows := searchRows(searchHit(1, 11, "цитата", 0.7)).RowError(0, errors.New("сбой чтения"))
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(rows)
		if _, _, err := s.SearchFragments(context.Background(), validRun, "запрос", "", 5, nil); err == nil {
			t.Fatal("ожидалась ошибка чтения")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSearchPassesTranscriptPunctuationAsOneSQLArgument(t *testing.T) {
	pool, s := testStore(t)
	run := testUUID(501)
	query := "О'Нил: <строка> \"цитата\" & 3 < 7;"
	quote := "  " + query + "\n"
	pool.ExpectQuery(`^SELECT ref_id, source_id, quote, score FROM find_fragments_bm25\(\$1,\$2,\$3\)$`).
		WithArgs(run, query, 5).WillReturnRows(searchRows(searchHit(1, 11, quote, 0.7)))
	hits, mode, err := s.SearchFragments(context.Background(), run, query, "", 5, nil)
	if err != nil || mode != "bm25" || len(hits) != 1 || hits[0].Quote != quote {
		t.Fatalf("поиск: %v %s %+v", err, mode, hits)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSearchHybridUnit(t *testing.T) {
	runID := testUUID(500)
	t.Run("отклоняет пустой запрос и неверный limit", func(t *testing.T) {
		pool, s := testStore(t)
		for _, input := range []struct {
			query string
			limit int
		}{{" ", 5}, {"запрос", 0}, {"запрос", 101}} {
			if _, _, err := s.SearchHybrid(context.Background(), runID, input.query, "", input.limit, nil); err == nil {
				t.Fatalf("ожидалась ошибка для %+v", input)
			}
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("BM25 обрезает список до limit", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(searchRows(searchHit(1, 11, "первый", 0.9), searchHit(2, 12, "второй", 0.8)))
		got, mode, err := s.SearchHybrid(context.Background(), runID, "запрос", "", 1, nil)
		if err != nil || mode != "bm25" || len(got) != 1 {
			t.Fatalf("неверный ответ: %v, %s, %+v", err, mode, got)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("объединяет ранги двух поисков", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_relevant_fragments").WithArgs(anyArgs(4)...).WillReturnRows(searchRows(searchHit(1, 11, "вектор", 0.9), searchHit(2, 12, "общий", 0.8)))
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnRows(searchRows(searchHit(2, 12, "общий", 99), searchHit(3, 13, "лексика", 98)))
		got, mode, err := s.SearchHybrid(context.Background(), runID, "запрос", "модель", 3, vectorEmbedding)
		if err != nil || mode != "hybrid" || len(got) != 3 || got[0].RefID != testUUID(2) {
			t.Fatalf("неверное объединение: %v, %s, %+v", err, mode, got)
		}
		if got[0].Score <= got[1].Score {
			t.Fatalf("общий результат должен иметь больший объединённый ранг: %+v", got)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("ошибка второго поиска возвращает hybrid mode", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("find_relevant_fragments").WithArgs(anyArgs(4)...).WillReturnRows(searchRows(searchHit(1, 11, "вектор", 0.9)))
		pool.ExpectQuery("find_fragments_bm25").WithArgs(anyArgs(3)...).WillReturnError(errors.New("сбой BM25"))
		if _, mode, err := s.SearchHybrid(context.Background(), runID, "запрос", "модель", 3, vectorEmbedding); err == nil || mode != "hybrid" {
			t.Fatalf("ожидалась ошибка гибридного поиска: %v, %s", err, mode)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSearchRejectsUnsuitableVectorsBeforeVectorQuery(t *testing.T) {
	const query = "поиск"
	const model = "embedding-fixture"
	runID := testUUID(500)
	for _, tc := range []struct {
		name   string
		values int
		value  float32
		mode   string
	}{
		{"нулевой вектор", 768, 0, "bm25"},
		{"NaN", 768, float32(math.NaN()), "bm25"},
		{"положительная бесконечность", 768, float32(math.Inf(1)), "bm25"},
		{"отрицательная бесконечность", 768, float32(math.Inf(-1)), "bm25"},
		{"неверная размерность", 3, 1, "bm25"},
		{"валидный вектор", 768, 1, "vector"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, s := testStore(t)
			vector := make([]float32, tc.values)
			vector[0] = tc.value
			if tc.mode == "vector" {
				expected := "[1" + strings.Repeat(",0", 767) + "]"
				pool.ExpectQuery("find_relevant_fragments").WithArgs(runID, expected, model, 5).WillReturnRows(searchRows())
			} else {
				pool.ExpectQuery("find_fragments_bm25").WithArgs(runID, query, 5).WillReturnRows(searchRows())
			}
			_, mode, err := s.SearchFragments(context.Background(), runID, query, model, 5,
				func(context.Context, string) ([]float32, error) { return vector, nil })
			if err != nil || mode != tc.mode {
				t.Fatalf("способ поиска: %q, %v", mode, err)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
