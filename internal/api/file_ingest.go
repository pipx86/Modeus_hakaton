package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/ingestion"
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
	report, err := s.importFiles.Run(r.Context(), pg.Pool, request.Sources)
	switch {
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
