package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/logging"

	"github.com/jackc/pgx/v5/pgxpool"
)

// startTrudvsemVacancyImporter imports public vacancies from the official
// «Работа России» open-data API. It remains disabled until a query is set.
func startTrudvsemVacancyImporter(ctx context.Context, pool *pgxpool.Pool) {
	queries := splitImportQueries(os.Getenv("TRUDVSEM_IMPORT_QUERY"))
	if pool == nil || len(queries) == 0 {
		return
	}

	hours := importIntervalHours(os.Getenv("TRUDVSEM_IMPORT_INTERVAL_H"), 24)
	pages := importPages(os.Getenv("TRUDVSEM_IMPORT_PAGES"))
	endpoint := strings.TrimSpace(os.Getenv("TRUDVSEM_IMPORT_URL"))
	if endpoint == "" {
		endpoint = ingestion.DefaultTrudvsemVacanciesURL
	}
	userAgent := strings.TrimSpace(os.Getenv("TRUDVSEM_USER_AGENT"))
	if userAgent == "" {
		userAgent = "Modeus/1.0 (vacancy research importer)"
	}
	client := ingestion.DefaultHTTPClient()

	importOnce := func() bool {
		allOK := true
		for _, query := range queries {
			items, err := ingestion.FetchTrudvsem(ctx, client, endpoint, query, userAgent, pages)
			if err != nil {
				logging.Logger.Warn("trudvsem import fetch failed", "query", query, "error", err)
				allOK = false
				continue
			}
			counts, err := ingestion.SaveTrudvsemVacancies(ctx, pool, items)
			if err != nil {
				logging.Logger.Warn("trudvsem import save failed", "query", query, "error", err)
				allOK = false
				continue
			}
			logging.Logger.Info("trudvsem import completed", "query", query, "inserted", counts.Inserted, "updated", counts.Updated, "skipped", counts.Skipped)
		}
		return allOK
	}

	go func() {
		wait := time.Duration(0)
		for {
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if importOnce() {
				wait = time.Duration(hours) * time.Hour
			} else {
				wait = 15 * time.Minute
			}
		}
	}()
}

func splitImportQueries(raw string) []string {
	var queries []string
	for _, query := range strings.Split(raw, ";") {
		if query = strings.TrimSpace(query); query != "" {
			queries = append(queries, query)
		}
	}
	return queries
}

func importIntervalHours(raw string, fallback int) int {
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func importPages(raw string) int {
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 && parsed <= 20 {
		return parsed
	}
	return 1
}
