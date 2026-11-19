package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/learning"
)

// LearningPlanner builds a beginner-oriented learning plan for a profession.
type LearningPlanner interface {
	Plan(ctx context.Context, profession string) (learning.Plan, error)
}

// handleGetLearningPlan returns the persisted plan of a learning run.
func (s *Server) handleGetLearningPlan(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	pgStore, ok := s.store.(*PGStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "план обучения требует PostgreSQL")
		return
	}
	runID := strings.TrimSpace(r.PathValue("id"))
	if runID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "не указан идентификатор исследования")
		return
	}
	plan, err := pgStore.GetLearningPlan(runID)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "план обучения ещё не готов")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить план обучения")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}
