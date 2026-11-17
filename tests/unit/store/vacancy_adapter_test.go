package store_test

import (
	"context"
	"errors"
	"testing"

	"activity-descriptor/internal/store"

	"github.com/pashagolub/pgxmock/v4"
)

func TestVacancyAdapterByProfession(t *testing.T) {
	columns := []string{"external_id", "id", "title", "content", "source_url"}

	t.Run("пустая профессия не обращается к базе", func(t *testing.T) {
		pool, s := testStore(t)
		_, err := store.NewVacancyAdapter(s).ByProfession(context.Background(), "  ", 5)
		if err == nil {
			t.Fatal("ожидалась ошибка пустой профессии")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	for _, tc := range []struct {
		name            string
		limit, expected int
	}{
		{"заданный лимит", 5, 5},
		{"нулевой лимит", 0, 100},
		{"отрицательный лимит", -1, 100},
		{"предельный лимит", 1000, 1000},
		{"лимит выше предела", 1001, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, s := testStore(t)
			pool.ExpectQuery("SELECT COALESCE").WithArgs("%аналитик%", tc.expected).
				WillReturnRows(pgxmock.NewRows(columns).AddRow("hh-1", "source-1", "Аналитик", "Описание", "https://example.org/1"))
			got, err := store.NewVacancyAdapter(s).ByProfession(context.Background(), "  аналитик  ", tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != "hh-1" || got[0].SourceID != "source-1" || got[0].Title != "Аналитик" || got[0].Description != "Описание" || got[0].SourceURL != "https://example.org/1" {
				t.Fatalf("неверные данные вакансии: %+v", got)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("ошибка запроса", func(t *testing.T) {
		pool, s := testStore(t)
		want := errors.New("ошибка запроса")
		pool.ExpectQuery("SELECT COALESCE").WithArgs("%аналитик%", 1).WillReturnError(want)
		_, err := store.NewVacancyAdapter(s).ByProfession(context.Background(), "аналитик", 1)
		if !errors.Is(err, want) {
			t.Fatalf("ожидалась ошибка запроса, получено: %v", err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ошибка преобразования строки", func(t *testing.T) {
		pool, s := testStore(t)
		pool.ExpectQuery("SELECT COALESCE").WithArgs("%аналитик%", 1).
			WillReturnRows(pgxmock.NewRows(columns[:4]).AddRow("hh-1", "source-1", "Аналитик", "Описание"))
		_, err := store.NewVacancyAdapter(s).ByProfession(context.Background(), "аналитик", 1)
		if err == nil {
			t.Fatal("ожидалась ошибка чтения строки")
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ошибка при чтении результата", func(t *testing.T) {
		pool, s := testStore(t)
		want := errors.New("ошибка чтения")
		rows := pgxmock.NewRows(columns).AddRow("hh-1", "source-1", "Аналитик", "Описание", "url").RowError(0, want)
		pool.ExpectQuery("SELECT COALESCE").WithArgs("%аналитик%", 1).WillReturnRows(rows)
		_, err := store.NewVacancyAdapter(s).ByProfession(context.Background(), "аналитик", 1)
		if !errors.Is(err, want) {
			t.Fatalf("ожидалась ошибка чтения, получено: %v", err)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
