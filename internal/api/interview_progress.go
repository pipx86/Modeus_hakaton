package api

import (
	"errors"
	"net/http"

	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/auth"
)

type interviewProgressChat interface {
	Progress(runID string) (interviewer.Progress, error)
}

// handleInterviewProgress godoc
// @Summary      Interview progress
// @Description  Returns asked/remaining question counts, covered and uncovered topics, answer-quality averages and GitHub contradictions for the active interview.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  interviewer.Progress
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/interview-progress [get]
func (s *Server) handleInterviewProgress(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	progressor, ok := s.chat.(interviewProgressChat)
	if !ok || s.chat == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "прогресс интервью недоступен")
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	progress, err := progressor.Progress(runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "интервью не найдено")
			return
		}
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "активное интервью не найдено")
		return
	}
	writeJSON(w, http.StatusOK, progress)
}
