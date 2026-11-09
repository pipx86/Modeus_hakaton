package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func echoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "no role in context", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(string(role)))
	})
}

func TestMiddlewareAllowsValidToken(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	tok, _ := Issue("u-1", RoleTeacher, time.Hour)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()

	Middleware(echoHandler()).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", rr.Code)
	}
	if rr.Body.String() != "teacher" {
		t.Fatalf("роль не долетела до handler: %q", rr.Body.String())
	}
}

func TestMiddlewareRejectsMissingHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	Middleware(echoHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 без заголовка, получили %d", rr.Code)
	}
}

func TestMiddlewareRejectsNonBearerPrefix(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic abc")
	rr := httptest.NewRecorder()
	Middleware(echoHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 на Basic, получили %d", rr.Code)
	}
}

func TestMiddlewareRejectsGarbageToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rr := httptest.NewRecorder()
	Middleware(echoHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 на мусор, получили %d", rr.Code)
	}
}

func TestMiddlewareRejectsExpiredToken(t *testing.T) {
	SetSecretForTests("test-secret-for-ci")
	tok, _ := Issue("u-2", RoleAdmin, -time.Second)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	Middleware(echoHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("ожидали 401 на expired, получили %d", rr.Code)
	}
}
