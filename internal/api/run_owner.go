package api

import (
	"net/http"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

// runOwnerCreator — optional Store extension: create a run attributed to a user.
type runOwnerCreator interface {
	CreateRunForUser(profession, userID string) (*domain.Run, error)
}

var _ runOwnerCreator = (*PGStore)(nil)
var _ runOwnerCreator = (*memStore)(nil)

func (m *memStore) CreateRunForUser(profession, userID string) (*domain.Run, error) {
	run, err := m.CreateRun(profession)
	if err != nil {
		return nil, err
	}
	if userID == "" {
		return run, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := m.runs[run.ID]
	if stored != nil {
		stored.CreatedBy = userID
		run.CreatedBy = userID
	}
	return run, nil
}

// ownerlessRunAllowed keeps runs created before ownership existed removable by
// users. New runs always get created_by from the JWT subject.
func ownerlessRunAllowed(run *domain.Run) bool {
	return run.CreatedBy == ""
}

func (s *Server) allowInterviewAccess(w http.ResponseWriter, r *http.Request, runID string) bool {
	run, err := s.store.GetRun(runID)
	if err != nil {
		if !mapStoreError(w, err) {
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось проверить доступ к интервью")
		}
		return false
	}
	if auth.RoleFromCtx(r.Context()) != auth.RoleAdmin &&
		!ownerlessRunAllowed(run) && run.CreatedBy != auth.Subject(r.Context()) {
		writeErr(w, http.StatusForbidden, "FORBIDDEN", "доступ к интервью есть у владельца и администратора")
		return false
	}
	return true
}
