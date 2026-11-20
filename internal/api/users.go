package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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

type userRoleRequest struct {
	Role string `json:"role"`
}

// handleUpdateUserRole godoc
// @Summary      Change a user's role
// @Description  Assigns admin, specialist or user to an existing account. Admin only.
// @Tags         users
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "User ID"
// @Param        body body      userRoleRequest true  "New role"
// @Success      200  {object}  authUserDTO
// @Failure      400  {object}  errorBody
// @Failure      404  {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/users/{id}/role [patch]
func (s *Server) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	store, ok := s.store.(authUserStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "управление пользователями недоступно")
		return
	}
	var req userRoleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "некорректный JSON")
		return
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role != string(auth.RoleAdmin) && role != string(auth.RoleUser) && role != string(auth.RoleSpecialist) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "роль должна быть admin, specialist или user")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := store.UpdateUserRole(r.Context(), id, role); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "пользователь не найден")
			return
		}
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось изменить роль")
		return
	}
	users, err := store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось загрузить пользователя")
		return
	}
	for i := range users {
		if users[i].ID == id {
			writeJSON(w, http.StatusOK, toAuthUser(&users[i]))
			return
		}
	}
	writeErr(w, http.StatusNotFound, "NOT_FOUND", "пользователь не найден")
}
