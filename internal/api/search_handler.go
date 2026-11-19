package api

import (
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/search"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) searchService() (search.Service, bool) {
	pg, ok := s.store.(*PGStore)
	if !ok {
		return search.Service{}, false
	}
	address := os.Getenv("OLLAMA_URL")
	if address == "" {
		address = "http://127.0.0.1:11434"
	}
	model := os.Getenv("OLLAMA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	return search.Service{Pool: pg.Pool, Model: embeddings.Ollama{BaseURL: address, Model: model}}, true
}
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	if _, err := s.store.GetRun(r.PathValue("id")); err != nil {
		writeTreeError(w, err)
		return
	}
	var id pgtype.UUID
	if err := id.Scan(r.PathValue("id")); err != nil {
		writeErr(w, 400, "BAD_REQUEST", "ожидается UUID")
		return
	}
	limit := 5
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeErr(w, 400, "BAD_REQUEST", "limit должен быть от 1 до 100")
			return
		}
	}
	service, ok := s.searchService()
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "нужна PostgreSQL")
		return
	}
	search := service.Search
	strategy := r.URL.Query().Get("strategy")
	if strategy == "hybrid" {
		search = service.HybridSearch
	} else if strategy != "" && strategy != "vector" {
		writeErr(w, 400, "BAD_REQUEST", "strategy должен быть vector или hybrid")
		return
	}
	hits, mode, err := search(r.Context(), id, r.URL.Query().Get("q"), limit)
	if err != nil {
		writeErr(w, 400, "SEARCH_FAILED", "не удалось выполнить поиск; проверьте запрос и пакет поиска")
		return
	}
	nodeByQuote := map[string]string{}
	if answers, answerErr := s.store.GetAnswers(r.PathValue("id")); answerErr == nil {
		for _, answer := range answers {
			if answer.NodeID != "" && answer.Quote != "" {
				nodeByQuote[answer.Quote] = answer.NodeID
			}
		}
	}
	enriched := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		item := map[string]any{
			"ref_id":    hit.RefID,
			"source_id": hit.SourceID,
			"quote":     hit.Quote,
			"score":     hit.Score,
		}
		if nodeID := nodeByQuote[hit.Quote]; nodeID != "" {
			item["node_id"] = nodeID
		}
		enriched = append(enriched, item)
	}
	writeJSON(w, 200, map[string]any{"mode": mode, "hits": enriched})
}
func (s *Server) handleIndexSource(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	var id pgtype.UUID
	if err := id.Scan(r.PathValue("id")); err != nil || !id.Valid {
		writeErr(w, 400, "BAD_REQUEST", "ожидается UUID")
		return
	}
	service, ok := s.searchService()
	if !ok {
		writeErr(w, 501, "NOT_IMPLEMENTED", "нужна PostgreSQL")
		return
	}
	if err := service.IndexSource(r.Context(), id); err != nil {
		writeErr(w, 502, "INDEX_FAILED", "не удалось получить векторы; прежний индекс сохранён")
		return
	}
	w.WriteHeader(204)
}
