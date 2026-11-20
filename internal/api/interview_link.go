package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"activity-descriptor/internal/auth"
)

type interviewLinkRequest struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// handleInterviewLink posts a link to a commit, merge request, document or
// portfolio as the respondent's next evidence message.
func (s *Server) handleInterviewLink(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	if s.chat == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "чат недоступен")
		return
	}
	var req interviewLinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	raw := strings.TrimSpace(req.URL)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите корректную http(s)-ссылку")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = parsed.Host
	}
	message, err := s.chat.Post(r.PathValue("id"), "Прикрепляю ссылку-доказательство «"+title+"»: "+raw)
	if err != nil {
		writeErr(w, http.StatusConflict, "CHAT_UNAVAILABLE", "не удалось добавить ссылку в интервью")
		return
	}
	writeJSON(w, http.StatusCreated, message)
}
