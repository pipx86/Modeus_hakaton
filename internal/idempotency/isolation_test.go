package idempotency

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
)

func TestКлючИзолированПоПользователюИМаршруту(t *testing.T) {
	auth.SetSecretForTests("test-only")
	calls := 0
	h := auth.Middleware(Middleware(NewMemStore(time.Minute), time.Minute, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-Result", "ok")
		w.WriteHeader(204)
	})))
	token1 := auth.TokenFor("first", auth.RoleTeacher)
	token2 := auth.TokenFor("second", auth.RoleTeacher)
	request := func(token, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "same")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, result := range []*httptest.ResponseRecorder{request(token1, "/a"), request(token1, "/a"), request(token2, "/a"), request(token1, "/b")} {
		if result.Code != 204 || result.Header().Get("X-Result") != "ok" {
			t.Fatalf("%d %v", result.Code, result.Header())
		}
	}
	if calls != 3 {
		t.Fatalf("ожидали 3 отдельных вызова, получено %d", calls)
	}
}

func TestРазныеПараллельныеЗапросыНеСмешиваются(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := Middleware(NewMemStore(time.Minute), time.Minute, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; w.WriteHeader(204) }))
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/x", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "key")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- request("a") }()
	<-started
	go func() { second <- request("b") }()
	close(release)
	if response := <-first; response.Code != 204 {
		t.Fatalf("первый: %d", response.Code)
	}
	if response := <-second; response.Code != 409 {
		t.Fatalf("второй: %d", response.Code)
	}
}

func TestРазныеРолиОдногоПользователяНеРазделяютОтвет(t *testing.T) {
	auth.SetSecretForTests("test-only")
	h := auth.Middleware(Middleware(NewMemStore(time.Minute), time.Minute, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.RequireRole(w, r, auth.RoleTeacher) {
			return
		}
		w.WriteHeader(204)
	})))
	request := func(role auth.Role) int {
		req := httptest.NewRequest("POST", "/x", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+auth.TokenFor("same-user", role))
		req.Header.Set("Idempotency-Key", "same")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := request(auth.RoleTeacher); code != 204 {
		t.Fatal(code)
	}
	if code := request(auth.RoleStudent); code != 403 {
		t.Fatalf("ограничение роли обойдено: %d", code)
	}
}
