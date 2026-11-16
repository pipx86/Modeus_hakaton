package auth

import (
	"context"
	"net/http"
)

func RequireRole(w http.ResponseWriter, r *http.Request, allowed ...Role) bool {
	role, ok := FromContext(r.Context())
	if !ok {
		writeForbidden(w, "no role in context")
		return false
	}
	for _, a := range allowed {
		if role == a {
			return true
		}
	}
	writeForbidden(w, "insufficient permissions for this action")
	return false
}

func RoleFromCtx(ctx context.Context) Role {
	r, _ := FromContext(ctx)
	return r
}

func writeForbidden(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"code":"FORBIDDEN","message":"` + msg + `"}`))
}
