package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/githubprofile"
)

type githubEvaluator interface {
	Evaluate(ctx context.Context, username, profession string) (githubprofile.Assessment, error)
}

// SetGitHubEvaluator wires the public GitHub profile reader. It is optional so
// the in-memory demo server and tests can run without network access.
func (s *Server) SetGitHubEvaluator(evaluator githubEvaluator) {
	s.github = evaluator
}

type githubEvaluateRequest struct {
	Username   string `json:"username"`
	Profession string `json:"profession,omitempty"`
}

// handleEvaluateGitHub godoc
// @Summary      Evaluate a GitHub profile
// @Description  Reads public repositories and returns languages, topics, projects, a deterministic quality score and an optional match against a profession. Uses GITHUB_TOKEN when configured.
// @Tags         github
// @Accept       json
// @Produce      json
// @Param        body body      githubEvaluateRequest true "GitHub username and optional profession"
// @Success      200  {object}  githubprofile.Assessment
// @Failure      400  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      429  {object}  errorBody
// @Failure      502  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/github/evaluate [post]
func (s *Server) handleEvaluateGitHub(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleSpecialist, auth.RoleUser) {
		return
	}
	if s.github == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "оценка GitHub не настроена")
		return
	}
	var req githubEvaluateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Profession = strings.TrimSpace(req.Profession)
	if req.Username == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите имя пользователя GitHub")
		return
	}
	assessment, err := s.github.Evaluate(r.Context(), req.Username, req.Profession)
	if err != nil {
		switch {
		case errors.Is(err, githubprofile.ErrNotFound):
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "пользователь GitHub не найден")
		case errors.Is(err, githubprofile.ErrRateLimited):
			writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "лимит GitHub исчерпан, попробуйте позже")
		case strings.Contains(err.Error(), "invalid username"):
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректное имя пользователя GitHub")
		default:
			writeErr(w, http.StatusBadGateway, "GITHUB_UNAVAILABLE", "не удалось получить данные GitHub")
		}
		return
	}
	writeJSON(w, http.StatusOK, assessment)
}
