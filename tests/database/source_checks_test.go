package database_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestАвтоматическиеПроверкиИсточника(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("нужна отдельная тестовая база в STORE_TEST_DATABASE_URL")
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

	var sourceID, runID, nodeID, evidenceID string
	if err := tx.QueryRow(ctx, `INSERT INTO sources(type,title) VALUES('document','тест') RETURNING id::text`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	assertSource := func(content, origin bool, urlFormat string) {
		t.Helper()
		var gotContent, gotOrigin bool
		var gotURL string
		if err := tx.QueryRow(ctx, `SELECT content_present, origin_present, url_format FROM source_checks WHERE source_id=$1`, sourceID).Scan(&gotContent, &gotOrigin, &gotURL); err != nil {
			t.Fatal(err)
		}
		if gotContent != content || gotOrigin != origin || gotURL != urlFormat {
			t.Fatalf("признаки источника: %v %v %q", gotContent, gotOrigin, gotURL)
		}
	}
	assertSource(false, false, "missing")
	if _, err := tx.Exec(ctx, `UPDATE sources SET content='Текст источника',provider='file',external_id='1',source_url='не ссылка' WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	assertSource(true, true, "invalid")
	if _, err := tx.Exec(ctx, `UPDATE sources SET source_url='https://example.org/a' WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	assertSource(true, true, "valid")
	if err := tx.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('тест') RETURNING id::text`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO nodes(run_id,type,title) VALUES($1,'role','роль') RETURNING id::text`, runID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO evidence(node_id,source_id,quote) VALUES($1,$2,'источника') RETURNING id::text`, nodeID, sourceID).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	assertQuote := func(want string) {
		t.Helper()
		var got string
		if err := tx.QueryRow(ctx, `SELECT result FROM evidence_quote_checks WHERE evidence_id=$1`, evidenceID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("проверка цитаты: получено %q, ожидалось %q", got, want)
		}
	}
	assertQuote("matched")
	if _, err := tx.Exec(ctx, `UPDATE evidence SET quote='нет в тексте' WHERE id=$1`, evidenceID); err != nil {
		t.Fatal(err)
	}
	assertQuote("not_matched")
	if _, err := tx.Exec(ctx, `UPDATE sources SET content=NULL WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	assertQuote("unavailable")
	assertSource(false, true, "valid")
	if _, err := tx.Exec(ctx, `UPDATE sources SET content='нет в тексте' WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	assertQuote("matched")
}
