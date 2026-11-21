package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/chat"
)

type postMessageRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	if s.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "CHAT_DISABLED", "chat is not configured")
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	messages, err := s.chat.Messages(runID)
	if errors.Is(err, chat.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "chat session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось прочитать интервью")
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	if s.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "CHAT_DISABLED", "chat is not configured")
		return
	}
	var req postMessageRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	runID := r.PathValue("id")
	if !s.allowInterviewAccess(w, r, runID) {
		return
	}
	message, err := s.chat.Post(runID, req.Content)
	switch {
	case errors.Is(err, chat.ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "chat session not found")
	case errors.Is(err, chat.ErrEmptyMessage):
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "content is required")
	case errors.Is(err, chat.ErrInvalidMessage):
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "ответ должен быть текстом без нулевого символа")
	case errors.Is(err, chat.ErrSessionFinished):
		// The interview is over (done, degraded or failed): the history stays
		// readable through GET, but the run does not accept new answers.
		writeErr(w, http.StatusConflict, "SESSION_FINISHED", "интервью уже завершено: история доступна только для чтения")
	case errors.Is(err, chat.ErrSessionInterrupted):
		// An active chat run from before durable session snapshots (or with a
		// corrupt snapshot) cannot be resumed; fail loudly instead of hanging.
		writeErr(w, http.StatusConflict, "SESSION_INTERRUPTED", "сессия интервью была прервана перезапуском сервера и не может быть продолжена")
	case errors.Is(err, chat.ErrQuestionPending):
		// The restored session is generating the next question; the client should
		// retry after the agent_message event arrives.
		writeErr(w, http.StatusConflict, "CHAT_CONFLICT", "интервьюер готовит следующий вопрос; повторите отправку после его появления")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось сохранить ответ")
	default:
		writeJSON(w, http.StatusCreated, message)
	}
}
