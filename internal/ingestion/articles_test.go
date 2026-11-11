package ingestion

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFetchArticles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "data science" || r.URL.Query().Get("filter") != "type:journal-article" {
			t.Error("неверные параметры поиска")
		}
		fmt.Fprint(w, `{"message":{"items":[{"DOI":"10.1234/Example","title":["Research"],"abstract":"<jats:p>Original <jats:italic>text</jats:italic></jats:p>"},{"DOI":"10.1234/example","title":["Duplicate"]}]}}`)
	}))
	defer server.Close()
	items, err := FetchArticles(context.Background(), server.Client(), server.URL, "data science", 10)
	if err != nil || len(items) != 1 || items[0].DOI != "10.1234/example" || items[0].Abstract != "Original text" {
		t.Fatalf("%+v %v", items, err)
	}
}

func TestSaveArticlesIsIdempotentAndLinksRun(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая БД")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var run string
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('проверка статей') RETURNING id::text`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	doi := fmt.Sprintf("10.1234/test-%d", time.Now().UnixNano())
	defer func() {
		pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run)
		pool.Exec(ctx, `DELETE FROM sources WHERE provider='crossref' AND external_id=$1`, doi)
	}()
	items := []Article{{DOI: doi, Title: "Название", Abstract: "Содержание"}}
	first, err := SaveArticles(ctx, pool, items, run)
	if err != nil || first.Inserted != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := SaveArticles(ctx, pool, items, run)
	if err != nil || second.Skipped != 1 {
		t.Fatalf("%+v %v", second, err)
	}
	var linked int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM run_sources rs JOIN sources s ON s.id=rs.source_id WHERE rs.run_id=$1 AND s.provider='crossref' AND s.external_id=$2`, run, doi).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("%d %v", linked, err)
	}
}
