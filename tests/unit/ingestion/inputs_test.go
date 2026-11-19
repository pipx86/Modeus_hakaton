package ingestion_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHHRequestAndPublicationDateWithoutNetwork(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("text") != "Go & PostgreSQL" || r.URL.Query().Get("page") != "0" ||
			r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("HH-User-Agent") != "test-agent" {
			t.Fatalf("неверный запрос: %s / %v", r.URL, r.Header)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
			`{"items":[{"id":"vac","name":"Engineer","published_at":"2026-11-18T10:00:00+0300"}],"pages":1}`,
		))}, nil
	})}
	got, err := ingestion.FetchHH(context.Background(), client, "https://test.invalid/vacancies", "Go & PostgreSQL", "test-agent", "fixture", 2)
	if err != nil || len(got) != 1 || got[0].PublishedAt != "2026-11-18T10:00:00+0300" {
		t.Fatalf("вакансии: %+v, %v", got, err)
	}
}

func TestImportValidationBeforeDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, []byte(`{"items":[{"name":"missing id"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	files := ingestion.ImportFiles{Vacancies: path}
	if _, err := files.Run(ctx, nil, []string{"standards", "vacancies"}); !errors.Is(err, ingestion.ErrFileSourceUnavailable) {
		t.Fatalf("ненастроенный стандарт: %v", err)
	}
	if _, err := files.Run(ctx, nil, []string{"vacancies"}); err == nil || !strings.Contains(err.Error(), "id и name") {
		t.Fatalf("неверный JSON вакансий: %v", err)
	}
	standard := ingestion.ImportFiles{Standard: path, StandardKey: "test"}
	if _, err := standard.Run(ctx, nil, []string{"standards"}); err == nil {
		t.Fatal("загрузчик принял JSON с неверной структурой")
	}
}

func TestVacancyTrendRejectsUnsupportedPeriod(t *testing.T) {
	begin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		query string
		end   time.Time
	}{
		{"  ", begin.AddDate(0, 1, 0)},
		{"Go", begin},
		{"Go", begin.AddDate(2, 0, 1)},
	} {
		if _, err := ingestion.VacancyPublicationTrend(context.Background(), nil, tc.query, begin, tc.end); err == nil {
			t.Fatalf("принят неверный период: %+v", tc)
		}
	}
}
