package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/search"
	"activity-descriptor/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// semanticCorpus — два непохожих по лексике источника. Семантический запрос
// ниже не делит с источником A ни одной словоформы, поэтому BM25 его не найдёт,
// а настоящая модель эмбеддингов должна поднять источник A на первое место.
const (
	semanticSourceA = "Специалист по анализу данных собирает требования, проектирует дашборды, визуализирует показатели продукта и готовит отчёты для руководства."
	semanticSourceB = "Инженер по охране труда проводит инструктаж, проверяет средства защиты и расследует несчастные случаи на производстве."
	semanticQuery   = "наглядное представление метрик и графиков для презентации"
	lexicalQuery    = "охрана труда и инструктаж"
)

// TestСемантическийПоискНастоящейМоделью проверяет полный контур на живой
// Ollama и PostgreSQL с migrations_optional/vector: индексация через реальный
// /api/embed, векторный режим, превосходство над BM25 на синонимичном запросе и
// деградация до BM25 при недоступной модели.
//
// Запуск (нужны отдельная тестовая БД с векторными миграциями и Ollama):
//
//	$env:VECTOR_TEST_DATABASE_URL = $env:DATABASE_URL
//	$env:OLLAMA_TEST_URL = "http://127.0.0.1:11434"
//	$env:OLLAMA_EMBED_MODEL = "nomic-embed-text"
//	go test -v -count=1 ./tests/database -run TestСемантическийПоиск
func TestСемантическийПоискНастоящейМоделью(t *testing.T) {
	dsn := os.Getenv("VECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("для проверки нужна VECTOR_TEST_DATABASE_URL")
	}
	address := os.Getenv("OLLAMA_TEST_URL")
	if address == "" {
		t.Skip("для проверки нужна настоящая Ollama в OLLAMA_TEST_URL")
	}
	model := os.Getenv("OLLAMA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var runID, sourceA, sourceB pgtype.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('семантический поиск') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title,content) VALUES('document','семантика A',$1) RETURNING id`, semanticSourceA).Scan(&sourceA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title,content) VALUES('document','семантика B',$1) RETURNING id`, semanticSourceB).Scan(&sourceB); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO run_sources(run_id,source_id) VALUES($1,$2),($1,$3)`, runID, sourceA, sourceB); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM run_sources WHERE run_id=$1`, runID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM sources WHERE id IN ($1,$2)`, sourceA, sourceB)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM runs WHERE id=$1`, runID)
	}()

	service := search.Service{Pool: pool, Model: search.Config{OllamaURL: address, EmbedModel: model}.Client()}
	for _, sourceID := range []pgtype.UUID{sourceA, sourceB} {
		indexStarted := time.Now()
		if err := service.IndexSource(ctx, sourceID); err != nil {
			t.Fatalf("индексация %s: %v", sourceID.String(), err)
		}
		t.Logf("indexed source=%s in %s", sourceID.String(), time.Since(indexStarted).Round(time.Millisecond))
	}
	var fragments, embeddings int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_fragments WHERE source_id IN ($1,$2)`, sourceA, sourceB).Scan(&fragments); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fragment_embeddings e JOIN source_fragments f ON f.id=e.fragment_id WHERE f.source_id IN ($1,$2) AND e.model=$3`, sourceA, sourceB, model).Scan(&embeddings); err != nil {
		t.Fatal(err)
	}
	if fragments == 0 || embeddings != fragments {
		t.Fatalf("фрагменты=%d векторы=%d модель=%s", fragments, embeddings, model)
	}

	// Векторный поиск: синонимичный запрос без общих словоформ с источником A.
	searchStarted := time.Now()
	hits, mode, err := service.Search(ctx, runID, semanticQuery, 5)
	elapsed := time.Since(searchStarted)
	if err != nil {
		t.Fatalf("векторный поиск: %v", err)
	}
	if mode != "vector" {
		t.Fatalf("режим=%s, ожидали vector: модель %s недоступна или вектор не построен", mode, model)
	}
	if len(hits) == 0 || hits[0].SourceID != sourceA {
		t.Fatalf("векторный поиск не поднял семантически близкий источник A: %+v", hits)
	}
	t.Logf("vector query=%q mode=%s hits=%d top_source=%s score=%.4f latency=%s", semanticQuery, mode, len(hits), hits[0].SourceID.String(), hits[0].Score, elapsed.Round(time.Millisecond))

	// Лексический BM25 на том же запросе: общих словоформ нет, совпадений ноль.
	bm25, bm25Mode, err := store.New(pool).SearchFragments(ctx, runID, semanticQuery, "", 5, nil)
	if err != nil {
		t.Fatalf("BM25: %v", err)
	}
	if bm25Mode != "bm25" {
		t.Fatalf("режим BM25=%s", bm25Mode)
	}
	if len(bm25) != 0 {
		t.Fatalf("BM25 неожиданно нашёл синонимичный запрос (%d hits): %+v", len(bm25), bm25)
	}
	control, controlMode, err := store.New(pool).SearchFragments(ctx, runID, lexicalQuery, "", 5, nil)
	if err != nil || controlMode != "bm25" || len(control) == 0 || control[0].SourceID != sourceB {
		t.Fatalf("лексический контрольный запрос: режим=%s hits=%+v err=%v", controlMode, control, err)
	}

	// Гибридный режим объединяет оба ранга и тоже возвращает источник A первым.
	hybrid, hybridMode, err := service.HybridSearch(ctx, runID, semanticQuery, 5)
	if err != nil || hybridMode != "hybrid" || len(hybrid) == 0 || hybrid[0].SourceID != sourceA {
		t.Fatalf("гибридный поиск: режим=%s hits=%+v err=%v", hybridMode, hybrid, err)
	}

	// Недоступная Ollama не должна ломать поиск: BM25-фолбэк и режим bm25.
	down := search.Service{Pool: pool, Model: search.Config{OllamaURL: "http://127.0.0.1:1", EmbedModel: model}.Client()}
	degradedHits, degradedMode, err := down.Search(ctx, runID, lexicalQuery, 5)
	if err != nil || degradedMode != "bm25" || len(degradedHits) == 0 || degradedHits[0].SourceID != sourceB {
		t.Fatalf("деградация до BM25: режим=%s hits=%+v err=%v", degradedMode, degradedHits, err)
	}
	t.Logf("degraded (Ollama down) mode=%s hits=%d top_source=%s", degradedMode, len(degradedHits), degradedHits[0].SourceID.String())
}
