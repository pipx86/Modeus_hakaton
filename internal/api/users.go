package api

import (
	"net/http"

	"activity-descriptor/internal/auth"
)

// demoUserInfo — учётная запись, для которой администратор выпускает JWT
// (cmd/local-token или cmd/test-users). Паролей в системе нет.
type demoUserInfo struct {
	Subject  string `json:"subject"`
	Role     string `json:"role"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

var demoUsers = []demoUserInfo{
	{Subject: "demo-admin", Role: string(auth.RoleAdmin)},
	{Subject: "demo-guest", Role: string(auth.RoleGuest), ReadOnly: true},
}

// handleListUsers godoc
// @Summary      List demo users
// @Description  Список демонстрационных пользователей и их ролей.
// @Tags         users
// @Produce      json
// @Success      200   {array}   demoUserInfo
// @Failure      403   {object}  errorBody
// @Security     BearerAuth
// @Router       /api/v1/users [get]
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin) {
		return
	}
	writeJSON(w, http.StatusOK, demoUsers)
}
