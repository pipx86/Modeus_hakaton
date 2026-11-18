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
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent, auth.RoleGuest) {
		return
	}
	if s.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "CHAT_DISABLED", "chat is not configured")
		return
	}
	runID := r.PathValue("id")
	messages, err := s.chat.Messages(runID)
	if errors.Is(err, chat.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "chat session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent) {
		return
	}
	if s.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "CHAT_DISABLED", "chat is not configured")
		return
	}
	var req postMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	message, err := s.chat.Post(r.PathValue("id"), req.Content)
	switch {
	case errors.Is(err, chat.ErrNotFound):
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "chat session not found")
	case errors.Is(err, chat.ErrEmptyMessage):
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "content is required")
	case err != nil:
		writeErr(w, http.StatusConflict, "CHAT_CONFLICT", err.Error())
	default:
		writeJSON(w, http.StatusCreated, message)
	}
}
