package database_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestВекторныйПоискФрагментов(t *testing.T) {
	url := os.Getenv("VECTOR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна VECTOR_TEST_DATABASE_URL и отдельная миграция поиска")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var run, other, source, otherSource, first, second, foreign pgtype.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('поиск') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('другое исследование') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','источник') RETURNING id`, run).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','другой источник') RETURNING id`, other).Scan(&otherSource); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,0,'подходящая цитата') RETURNING id`, source).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,1,'другая цитата') RETURNING id`, source).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,0,'чужая цитата') RETURNING id`, otherSource).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	vector := func(index int) string {
		values := make([]string, 768)
		for i := range values {
			values[i] = "0"
		}
		values[index] = "1"
		return "[" + strings.Join(values, ",") + "]"
	}
	a, b := vector(0), vector(1)
	if _, err = tx.Exec(ctx, `INSERT INTO fragment_embeddings(fragment_id,model,embedding) VALUES($1,'модель 1',$4::vector),($2,'модель 1',$5::vector),($3,'модель 1',$4::vector),($2,'модель 2',$4::vector)`, first, second, foreign, a, b); err != nil {
		t.Fatal(err)
	}
	var ref, returnedSource pgtype.UUID
	var quote string
	var score float64
	if err = tx.QueryRow(ctx, `SELECT ref_id, source_id, quote, score FROM find_relevant_fragments($1,$2::vector,'модель 1',1)`, run, a).Scan(&ref, &returnedSource, &quote, &score); err != nil {
		t.Fatal(err)
	}
	if ref != first || returnedSource != source || quote != "подходящая цитата" || math.Abs(score-1) > 0.00001 {
		t.Fatalf("неверный результат: %v %v %s %f", ref, returnedSource, quote, score)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM find_relevant_fragments($1,$2::vector,'модель 1',10)`, run, a).Scan(&count); err != nil || count != 2 {
		t.Fatalf("неверная область поиска: %d %v", count, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO fragment_embeddings(fragment_id,model,embedding) VALUES($1,'неверный размер','[1,0]'::vector)`, first); err == nil {
		t.Fatal("вектор неверной размерности принят")
	}
	// Неверная размерность прерывает транзакцию, поэтому измерение выполняется отдельно.
}

func TestВремяВекторногоПоиска(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var run, source pgtype.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('скорость') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','скорость') RETURNING id`, run).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) SELECT $1,i,'фрагмент '||i FROM generate_series(1,100) i`, source); err != nil {
		t.Fatal(err)
	}
	vector := "[1" + strings.Repeat(",0", 767) + "]"
	if _, err := tx.Exec(ctx, `INSERT INTO fragment_embeddings(fragment_id,model,embedding) SELECT id,'тест',$2::vector FROM source_fragments WHERE source_id=$1`, source, vector); err != nil {
		t.Fatal(err)
	}
	var total time.Duration
	for i := 0; i < 10; i++ {
		start := time.Now()
		var count int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM find_relevant_fragments($1,$2::vector,'тест',10)`, run, vector).Scan(&count)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if count != 10 {
			t.Fatalf("неверное количество результатов: %d", count)
		}
		if elapsed >= 2*time.Second {
			t.Fatal(fmt.Sprintf("запрос %d: %v", i+1, elapsed))
		}
		total += elapsed
	}
	t.Logf("10 запросов к тестовой базе: %v; проверка без вызова модели", total)
}

func TestЗапаснойПоискBM25(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var run, other, source, otherSource, first, second pgtype.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('тест BM25') RETURNING id`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('другое исследование') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','источник') RETURNING id`, run).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO sources(run_id,type,title) VALUES($1,'document','другой источник') RETURNING id`, other).Scan(&otherSource); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,1,'данные данные проект') RETURNING id`, source).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,2,'данные проект обзор') RETURNING id`, source).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO source_fragments(source_id,ordinal,quote) VALUES($1,1,'данные данные данные')`, otherSource); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT ref_id,source_id,quote,score FROM find_fragments_bm25($1,'данные',10)`, run)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []pgtype.UUID
	var scores []float64
	for rows.Next() {
		var id, sid pgtype.UUID
		var quote string
		var score float64
		if err = rows.Scan(&id, &sid, &quote, &score); err != nil {
			t.Fatal(err)
		}
		if sid != source {
			t.Fatal("вернулся источник другого исследования")
		}
		ids = append(ids, id)
		scores = append(scores, score)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != first || ids[1] != second || scores[0] <= scores[1] || scores[1] <= 0 {
		t.Fatalf("неверное ранжирование BM25: %v %v", ids, scores)
	}
	if math.Abs(scores[0]-math.Log(1.2)*1.375) > 0.00001 || math.Abs(scores[1]-math.Log(1.2)) > 0.00001 {
		t.Fatalf("ошибка формулы BM25: %v", scores)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM find_fragments_bm25($1,'несуществующееслово',10)`, run).Scan(&count); err != nil || count != 0 {
		t.Fatalf("пустой поиск: %d %v", count, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM find_fragments_bm25($1,'данные',1)`, run).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ограничение количества: %d %v", count, err)
	}
}
