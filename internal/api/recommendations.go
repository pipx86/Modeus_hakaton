package api

import (
	"net/http"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
	"activity-descriptor/internal/recommend"
)

// handleGetRecommendations godoc
// @Summary      Get post-pipeline recommendations for a run
// @Description  Returns deterministic recommendations for a finished run: two free Stepik courses and a personal development plan tailored to the profession, detected level and competency tree. Available only for done/degraded runs.
// @Tags         runs
// @Produce      json
// @Param        id   path      string  true  "Run ID"
// @Success      200  {object}  recommend.Recommendation
// @Failure      401  {object}  errorBody
// @Failure      403  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Failure      409  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/runs/{id}/recommendations [get]
func (s *Server) handleGetRecommendations(w http.ResponseWriter, r *http.Request) {
	// Рекомендации читают все роли, включая гостя демонстрационного стенда.
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent, auth.RoleGuest) {
		return
	}

	id := r.PathValue("id")

	run, err := s.store.GetRun(id)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("recommendations_get_run_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	// Рекомендации имеют смысл только после завершения пайплайна.
	switch run.Status {
	case domain.RunDone, domain.RunDegraded:
		// готово
	default:
		writeErr(w, http.StatusConflict, "RUN_NOT_READY", "run is not ready for recommendations")
		return
	}

	nodes, err := s.store.GetTree(run.ID)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("recommendations_get_tree_error", "run_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	answers, err := s.store.GetAnswers(run.ID)
	if err != nil {
		// Ответы — вспомогательные данные: без них план строится по профессии
		// и дереву компетенций, поэтому 500 здесь не нужен.
		logging.Logger.Warn("recommendations_answers_fallback", "run_id", id, "error", err)
		answers = nil
	}

	writeJSON(w, http.StatusOK, recommend.Build(run.Profession, nodes, answers))
}
