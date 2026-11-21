package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"activity-descriptor/internal/auth"
)

func (s *Server) changeReviewAccess(w http.ResponseWriter, r *http.Request, revoke bool) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleUser) {
		return
	}
	runID := r.PathValue("id")
	run, err := s.store.GetRun(runID)
	if err != nil {
		if !mapStoreError(w, err) {
			writeErr(w, 500, "INTERNAL", "не удалось прочитать исследование")
		}
		return
	}
	if auth.RoleFromCtx(r.Context()) != auth.RoleAdmin && run.CreatedBy != auth.Subject(r.Context()) {
		writeErr(w, 403, "FORBIDDEN", "доступ назначает владелец исследования или администратор")
		return
	}
	p, ok := requirePG(w, s)
	if !ok {
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || !emailPattern.MatchString(strings.TrimSpace(body.Email)) {
		writeErr(w, 400, "BAD_REQUEST", "укажите email специалиста")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	query := `INSERT INTO run_review_access(run_id,user_id) SELECT $1,u.id FROM users u JOIN roles r ON r.id=u.role_id WHERE lower(u.email)=$2 AND r.code='specialist' ON CONFLICT DO NOTHING`
	if revoke {
		query = `DELETE FROM run_review_access WHERE run_id=$1 AND user_id IN (SELECT id FROM users WHERE lower(email)=$2)`
	}
	tag, err := p.Pool.Exec(r.Context(), query, runID, email)
	if err != nil {
		writeErr(w, 500, "INTERNAL", "не удалось изменить доступ")
		return
	}
	if !revoke && tag.RowsAffected() == 0 {
		var exists bool
		if err := p.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM run_review_access a JOIN users u ON u.id=a.user_id JOIN roles r ON r.id=u.role_id WHERE a.run_id=$1 AND lower(u.email)=$2 AND r.code='specialist')`, runID, email).Scan(&exists); err != nil || !exists {
			writeErr(w, 404, "NOT_FOUND", "специалист не найден")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) handleGrantReviewAccess(w http.ResponseWriter, r *http.Request) {
	s.changeReviewAccess(w, r, false)
}
func (s *Server) handleRevokeReviewAccess(w http.ResponseWriter, r *http.Request) {
	s.changeReviewAccess(w, r, true)
}
