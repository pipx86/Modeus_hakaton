package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func guardedHandler(allowed []Role) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !RequireRole(w, r, allowed...) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func wrapWithRole(h http.Handler, role Role) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithRole(r.Context(), role)))
	})
}

func TestGuardAllowsMatchingRole(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler := wrapWithRole(guardedHandler([]Role{RoleAdmin, RoleTeacher}), RoleTeacher)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("ожидали 200 ok, получили %d %q", rr.Code, rr.Body.String())
	}
}

func TestGuardRejectsNonMatchingRole(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler := wrapWithRole(guardedHandler([]Role{RoleAdmin}), RoleStudent)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("ожидали 403, получили %d", rr.Code)
	}
}

func TestGuardRejectsNoRoleInContext(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	guardedHandler([]Role{RoleAdmin}).ServeHTTP(rr, req) // без обёртки WithRole
	if rr.Code != http.StatusForbidden {
		t.Fatalf("ожидали 403 при отсутствии роли, получили %d", rr.Code)
	}
}

func TestGuardMultipleAllowedRoles(t *testing.T) {
	allowed := []Role{RoleAdmin, RoleTeacher, RoleROP}
	for _, r := range allowed {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		wrapWithRole(guardedHandler(allowed), r).ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("роль %s должна была пройти, получили %d", r, rr.Code)
		}
	}
	// student не в списке
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	wrapWithRole(guardedHandler(allowed), RoleStudent).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("student должен был получить 403, получил %d", rr.Code)
	}
}
