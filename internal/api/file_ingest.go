package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/idempotency"
	"activity-descriptor/internal/ingestion"
	"activity-descriptor/internal/store"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
)

func (s *Server) SetImportFiles(files ingestion.ImportFiles) {
	s.importFiles = files
}

func (s *Server) handleFileImport(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	var request struct {
		Sources []string `json:"sources"`
	}
	body := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := body.Decode(&request); err != nil || body.Decode(new(any)) != io.EOF {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "нужен один JSON-объект со списком источников")
		return
	}
	pg, ok := s.store.(*PGStore)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "INGEST_UNAVAILABLE", "нужен PostgreSQL")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = r.Header.Get("X-Run-ID")
	}
	var report ingestion.FileImportReport
	var err error
	if key == "" {
		report, err = s.importFiles.Run(r.Context(), pg.Pool, request.Sources)
	} else {
		if len(key) > 256 || strings.TrimSpace(key) == "" || auth.Subject(r.Context()) == "" {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "недопустимый ключ импорта")
			return
		}
		report, err = s.runFileImportWithKey(r.Context(), pg.Pool, key, request.Sources)
	}
	switch {
	case errors.Is(err, errFileImportKeyConflict):
		writeErr(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", err.Error())
	case errors.Is(err, ingestion.ErrInvalidFileSources):
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ingestion.ErrFileSourceUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "INGEST_UNAVAILABLE", err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "INGEST_FAILED", "не удалось загрузить настроенные файлы")
	default:
		writeJSON(w, http.StatusOK, report)
	}
}

var errFileImportKeyConflict = errors.New("ключ импорта уже использован для другого списка источников")

func (s *Server) runFileImportWithKey(ctx context.Context, pool store.Database, key string, sources []string) (ingestion.FileImportReport, error) {
	var report ingestion.FileImportReport
	if pool == nil {
		return report, ingestion.ErrFileSourceUnavailable
	}
	ordered := slices.Clone(sources)
	slices.Sort(ordered)
	request, err := json.Marshal(ordered)
	if err != nil {
		return report, err
	}
	bodyHash := idempotency.HashBody(request)
	scope := idempotency.HashBody([]byte(auth.Subject(ctx) + "\x00" + key))

	tx, err := pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(context.Background())
	queries := db.New(tx)
	if err = queries.LockFileImportRequest(ctx, scope); err != nil {
		return report, err
	}
	previous, err := queries.GetFileImportResult(ctx, scope)
	if err == nil {
		if previous.BodyHash != bodyHash {
			return report, errFileImportKeyConflict
		}
		err = json.Unmarshal(previous.Report, &report)
		return report, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return report, err
	}
	// Загрузчики открывают savepoint. Записи и итог HTTP-вызова фиксирует общая транзакция.
	report, err = s.importFiles.Run(ctx, tx, sources)
	if err != nil {
		return ingestion.FileImportReport{}, err
	}
	result, err := json.Marshal(report)
	if err != nil {
		return ingestion.FileImportReport{}, err
	}
	if err = queries.SaveFileImportResult(ctx, db.SaveFileImportResultParams{ScopeKey: scope, BodyHash: bodyHash, Report: result}); err != nil {
		return ingestion.FileImportReport{}, err
	}
	if err = queries.PruneExpiredFileImportResults(ctx); err != nil {
		return ingestion.FileImportReport{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ingestion.FileImportReport{}, err
	}
	return report, nil
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	pg, ok := s.store.(*PGStore)
	if !ok || pg.Pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "problems": []string{"db_pool_unavailable"}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var currentSchema bool
	err := pg.Pool.QueryRow(ctx,
		"SELECT to_regclass('vacancy_catalog') IS NOT NULL AND to_regclass('file_import_results') IS NOT NULL AND to_regclass('skill_frequency_snapshots') IS NOT NULL AND to_regclass('publication_notifications') IS NOT NULL",
	).Scan(&currentSchema)
	switch {
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "problems": []string{"db_unavailable"}})
	case !currentSchema:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "problems": []string{"schema_not_ready"}})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "problems": []string{}})
	}
}
