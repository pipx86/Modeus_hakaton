package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/learning"
)

// LearningPlanner builds a beginner-oriented learning plan for a profession.
type LearningPlanner interface {
	Plan(ctx context.Context, profession string) (learning.Plan, error)
}

type learningPlanRequest struct {
	Profession string `json:"profession"`
}

// handleLearningPlan creates a beginner-oriented learning plan for one
// profession. It uses the same vacancy/material context as the interviewer and
// does not create a run: this is a lightweight "what should I learn" path.
func (s *Server) handleLearningPlan(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser, auth.RoleSpecialist) {
		return
	}
	if s.learningPlan == nil {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "генератор плана обучения не настроен")
		return
	}
	var req learningPlanRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "после тела запроса есть лишние данные")
		return
	}
	profession := strings.TrimSpace(req.Profession)
	if len([]rune(profession)) < 2 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите профессию")
		return
	}
	if len([]rune(profession)) > 200 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "слишком длинное название профессии")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	plan, err := s.learningPlan.Plan(ctx, profession)
	switch {
	case errors.Is(err, learning.ErrUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "UNAVAILABLE", "языковая модель недоступна")
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "LLM_ERROR", "не удалось составить план обучения")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}
