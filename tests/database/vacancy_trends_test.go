package database_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestТрендПубликацийВакансий(t *testing.T) {
	dsn := os.Getenv("STORE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая база в STORE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	title := fmt.Sprintf("Market-trend-fixture-%d", time.Now().UnixNano())
	defer pool.Exec(ctx, `DELETE FROM sources WHERE type='vacancy' AND title=$1`, title)

	items := []ingestion.Vacancy{
		{ID: title + "-jan", Name: title, PublishedAt: "2026-02-01T00:30:00+0300"},
		{ID: title + "-feb", Name: title, PublishedAt: "2026-02-17T12:00:00Z"},
		{ID: title + "-march", Name: title, PublishedAt: "2026-03-01T00:00:00Z"},
		{ID: title + "-unknown", Name: title},
	}
	first, err := ingestion.SaveVacancies(ctx, pool, items)
	if err != nil || first.Inserted != 4 {
		t.Fatalf("импорт: %+v, %v", first, err)
	}
	items[0].PublishedAt = ""
	repeat, err := ingestion.SaveVacancies(ctx, pool, items[:1])
	if err != nil || repeat.Skipped != 1 {
		t.Fatalf("ответ без даты: %+v, %v", repeat, err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := from.AddDate(0, 2, 0)
	trend, err := ingestion.VacancyPublicationTrend(ctx, pool, title, from, until)
	expected := []ingestion.VacancyMonth{{Month: "2026-01", Provider: "hh", Count: 1}, {Month: "2026-02", Provider: "hh", Count: 1}}
	if err != nil || !reflect.DeepEqual(trend.ObservedMonthly, expected) || trend.WithoutDate != 1 {
		t.Fatalf("тренд: %+v, %v", trend, err)
	}
}
