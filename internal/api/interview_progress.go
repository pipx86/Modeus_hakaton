package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
)

type interviewProgressChat interface {
	Progress(runID string) (chat.InterviewProgress, error)
}

type interviewContinuationChat interface {
	DecideContinuation(runID string, continueInterview bool) error
}

// handleInterviewProgress godoc
// @Summary      Interview progress
// @Description  Returns asked/remaining question counts, covered and uncovered topics, answer-quality averages and GitHub contradictions for the active interview.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  chat.InterviewProgress
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

func (s *Server) handleInterviewContinuation(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	decider, ok := s.chat.(interviewContinuationChat)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "CHAT_DISABLED", "интервью недоступно")
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	var req struct {
		Continue *bool `json:"continue"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Continue == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите continue: true или false")
		return
	}
	err := decider.DecideContinuation(runID, *req.Continue)
	switch {
	case errors.Is(err, chat.ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "активное интервью не найдено")
	case errors.Is(err, chat.ErrContinuationUnavailable), errors.Is(err, chat.ErrSessionFinished),
		errors.Is(err, chat.ErrSessionInterrupted), errors.Is(err, chat.ErrVersionConflict):
		writeErr(w, http.StatusConflict, "CONTINUATION_UNAVAILABLE", "выбор больше недоступен. Обновите состояние интервью.")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить выбор")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
