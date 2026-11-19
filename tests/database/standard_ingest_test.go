package database_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestПовторныйИмпортПрофстандарта(t *testing.T) {
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна INGEST_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	key := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer pool.Exec(ctx, "DELETE FROM sources WHERE provider='file' AND type='standard' AND external_id=$1", "standard:"+key)
	doc := ingestion.Standard{Title: "Профстандарт", Content: "Текст"}
	first, err := ingestion.SaveStandard(ctx, pool, key, doc)
	if err != nil || first.Inserted != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := ingestion.SaveStandard(ctx, pool, key, doc)
	if err != nil || second.Skipped != 1 {
		t.Fatalf("%+v %v", second, err)
	}
}
