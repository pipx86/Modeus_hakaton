package api

import (
	"context"
	"net/http"
	"time"

	"activity-descriptor/internal/auth"
)

// sourceQualityItem - технические и фактические признаки одного источника.
// Оценку в баллах считает клиент, чтобы критерии были прозрачны в интерфейсе.
type sourceQualityItem struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Title          string    `json:"title"`
	ContentChars   int       `json:"content_chars"`
	CreatedAt      time.Time `json:"created_at"`
	ContentPresent bool      `json:"content_present"`
	OriginPresent  bool      `json:"origin_present"`
	URLFormat      string    `json:"url_format"`
	Citations      int       `json:"citations"`
	QuoteChecked   int       `json:"quote_checked"`
	QuoteMatched   int       `json:"quote_matched"`
}

type sourceQualityReader interface {
	ListSourceQuality(context.Context) ([]sourceQualityItem, error)
}

// handleSourceQuality отдаёт признаки источников для админской оценки.
func (s *Server) handleSourceQuality(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	reader, ok := s.store.(sourceQualityReader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "оценка источников недоступна")
		return
	}
	items, err := reader.ListSourceQuality(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось оценить источники")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
