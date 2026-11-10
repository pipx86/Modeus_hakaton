package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestСерверТребуетМиграцию(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая PostgreSQL")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", "pg_catalog")
	u.RawQuery = q.Encode()
	// Проверяем пустую область поиска без удаления таблиц общей тестовой базы.
	err = run(context.Background(), config{address: "127.0.0.1:0", databaseURL: u.String(), jwtSecret: "test-only"})
	if err == nil || !strings.Contains(err.Error(), "миграции") {
		t.Fatalf("ожидалась понятная ошибка схемы: %v", err)
	}
}
