package api

import (
	"net/http"

	"activity-descriptor/internal/auth"
)

// handleDegradedReason explains a degraded result, including the deterministic
// emergency fallback. It is separate from the run payload so historical run
// queries stay unchanged.
func (s *Server) handleDegradedReason(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	pgStore, ok := s.store.(*PGStore)
	if !ok || pgStore.Pool == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "пояснение доступно только в PostgreSQL")
		return
	}
	reason, err := pgStore.DegradedReason(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "исследование не найдено")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reason": reason})
}
