package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("API_TOKEN", "")
	t.Setenv("HTTP_ADDR", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing configuration accepted")
	}
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("API_TOKEN", "test-token")
	c, err := loadConfig()
	if err != nil || c.address != "127.0.0.1:8080" {
		t.Fatalf("config: %+v, %v", c, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	c, err = loadConfig()
	if err != nil || c.address != "127.0.0.1:9090" {
		t.Fatalf("custom address: %+v, %v", c, err)
	}
}
func TestAuthentication(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := authenticate(next, "test-token")
	for _, tc := range []struct {
		path, auth string
		want       int
	}{
		{"/api/v1/health", "", 204}, {"/api/v1/runs", "", 401}, {"/api/v1/runs", "Bearer wrong", 401}, {"/api/v1/runs", "Bearer test-token", 204},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.auth != "" {
			r.Header.Set("Authorization", tc.auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %q: %d", tc.path, tc.auth, w.Code)
		}
	}
}
func TestRunRejectsBadDatabase(t *testing.T) {
	err := run(context.Background(), config{address: "127.0.0.1:0", databaseURL: "not-a-postgres-url", apiToken: "test-token"})
	if err == nil || strings.Contains(err.Error(), "not-a-postgres-url") {
		t.Fatalf("unexpected error: %v", err)
	}
}
