package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMeasureCountsSuccessAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	r, err := measure(context.Background(), &http.Client{Timeout: time.Second}, server.URL+"?secret=hidden", "test", 20, 4)
	if err != nil || r.Success != 20 || r.Errors != 0 || r.StatusCodes[204] != 20 || r.P99MS < r.MedianMS || r.Target != server.URL {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = measure(context.Background(), &http.Client{Timeout: time.Second}, server.URL, "wrong", 3, 1)
	if err != nil || r.Errors != 3 || r.StatusCodes[401] != 3 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestLoadAgainstPostgresAPI(t *testing.T) {
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
	if err = pool.QueryRow(ctx, `INSERT INTO runs(profession) VALUES('тест нагрузки') RETURNING id::text`).Scan(&run); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM runs WHERE id=$1`, run)
	server := httptest.NewServer(api.NewServer(api.NewPGStore(pool)).Routes())
	defer server.Close()
	token := auth.TokenFor("нагрузочный-тест", auth.RoleTeacher)
	report, err := measure(ctx, &http.Client{Timeout: 5 * time.Second}, server.URL+"/api/v1/runs/"+run, token, 100, 5)
	if err != nil || report.Errors != 0 || report.Success != 100 {
		t.Fatalf("%+v %v", report, err)
	}
	t.Logf("100 запросов чтения исследования, параллельность 5: %.2f запросов/с; p50 %.2f мс, p95 %.2f мс, p99 %.2f мс", report.RequestsPerSecond, report.MedianMS, report.P95MS, report.P99MS)
}
