package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"activity-descriptor/internal/store/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestВнешнийИдентификаторИсточника(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна STORE_TEST_DATABASE_URL")
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
	q := db.New(tx)
	args := db.UpsertExternalSourceParams{Type: "vacancy", Title: "первая версия", Provider: pgtype.Text{String: "hh.ru", Valid: true}, ExternalID: pgtype.Text{String: "12345", Valid: true}}
	one, err := q.UpsertExternalSource(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	args.Title = "новая версия"
	two, err := q.UpsertExternalSource(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID || two.Title != "новая версия" {
		t.Fatal("повторный импорт создал дубль или не обновил запись")
	}
	args.Provider = pgtype.Text{String: "other.example", Valid: true}
	other, err := q.UpsertExternalSource(ctx, args)
	if err != nil || other.ID == one.ID {
		t.Fatalf("разные поставщики: %v %v", other, err)
	}
	args.ExternalID = pgtype.Text{}
	if _, err = q.UpsertExternalSource(ctx, args); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("отсутствующий внешний ID должен отклоняться: %v", err)
	}
}

func TestПараллельныйИмпортБезДублей(t *testing.T) {
	url := os.Getenv("STORE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("для проверки нужна STORE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var suffix string
	if err = pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&suffix); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM sources WHERE provider='hh.ru' AND external_id=$1`, suffix)
	args := db.UpsertExternalSourceParams{Type: "vacancy", Title: "параллельный импорт", Provider: pgtype.Text{String: "hh.ru", Valid: true}, ExternalID: pgtype.Text{String: suffix, Valid: true}}
	const workers = 5
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := db.New(pool).UpsertExternalSource(ctx, args); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM sources WHERE provider='hh.ru' AND external_id=$1`, suffix).Scan(&count); err != nil || count != 1 {
		t.Fatalf("получено записей: %d, ошибка: %v", count, err)
	}
}
