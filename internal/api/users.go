package api

import (
	"net/http"

	"activity-descriptor/internal/auth"
)

// handleListUsers godoc
// @Summary      List registered users
// @Description  Returns registered accounts (id, email, display name, role). Admin only.
// @Tags         users
// @Produce      json
// @Success      200   {array}   authUserDTO
// @Failure      403   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/users [get]
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.store.(authUserStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "список пользователей недоступен")
		return
	}
	users, err := store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить пользователей")
		return
	}
	out := make([]authUserDTO, 0, len(users))
	for i := range users {
		out = append(out, toAuthUser(&users[i]))
	}
	writeJSON(w, http.StatusOK, out)
}
