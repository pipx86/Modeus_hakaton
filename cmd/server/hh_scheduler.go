package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/logging"

	"github.com/jackc/pgx/v5/pgxpool"
)

// startHHVacancyImporter runs a best-effort background import of hh.ru
// vacancies. It is disabled unless HH_IMPORT_QUERY and application credentials
// are configured. The token source refreshes the access token automatically.
func startHHVacancyImporter(ctx context.Context, pool *pgxpool.Pool) {
	queries := strings.Split(os.Getenv("HH_IMPORT_QUERY"), ";")
	var activeQueries []string
	for _, query := range queries {
		if query = strings.TrimSpace(query); query != "" {
			activeQueries = append(activeQueries, query)
		}
	}
	userAgent := strings.TrimSpace(os.Getenv("HH_USER_AGENT"))
	if pool == nil || len(activeQueries) == 0 || userAgent == "" {
		return
	}
	if strings.TrimSpace(os.Getenv("HH_CLIENT_ID")) == "" ||
		strings.TrimSpace(os.Getenv("HH_CLIENT_SECRET")) == "" {
		logging.Logger.Info("hh import disabled", "reason", "credentials are not configured")
		return
	}
	hours := 24
	if raw := strings.TrimSpace(os.Getenv("HH_IMPORT_INTERVAL_H")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			hours = parsed
		}
	}
	pages := 1
	if raw := strings.TrimSpace(os.Getenv("HH_IMPORT_PAGES")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 20 {
			pages = parsed
		}
	}
	client := ingestion.DefaultHTTPClient()
	tokens := ingestion.NewHHTokenSource(
		client,
		os.Getenv("HH_TOKEN_URL"),
		os.Getenv("HH_CLIENT_ID"),
		os.Getenv("HH_CLIENT_SECRET"),
		userAgent,
		os.Getenv("HH_ACCESS_TOKEN"),
	)
	cachePath := strings.TrimSpace(os.Getenv("HH_TOKEN_CACHE"))
	if cachePath == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cachePath = filepath.Join(dir, "ModeusProject", "hh-token.json")
		}
	}
	tokens.SetCachePath(cachePath)
	importOnce := func() bool {
		token, err := tokens.Token(ctx)
		if err != nil {
			logging.Logger.Warn("hh import token failed", "error", err)
			return false
		}
		allOK := true
		for _, query := range activeQueries {
			items, err := ingestion.FetchHH(ctx, client, "https://api.hh.ru/vacancies", query, userAgent, token, pages)
			if err != nil {
				logging.Logger.Warn("hh import fetch failed", "query", query, "error", err)
				allOK = false
				continue
			}
			counts, err := ingestion.SaveVacancies(ctx, pool, items)
			if err != nil {
				logging.Logger.Warn("hh import save failed", "query", query, "error", err)
				allOK = false
				continue
			}
			logging.Logger.Info("hh import completed", "query", query, "inserted", counts.Inserted, "updated", counts.Updated, "skipped", counts.Skipped)
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
			ok := importOnce()
			if ok {
				wait = time.Duration(hours) * time.Hour
			} else {
				// hh.ru may reject a token refresh while the previous token is
				// still alive; retry soon instead of waiting a whole day.
				wait = 15 * time.Minute
			}
		}
	}()
}
