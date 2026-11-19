package ingestion_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/ingestion"
)

func TestHHTokenSourceReusesDiskCacheAfterRestart(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "hh-token.json")
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"cached-token","token_type":"bearer","expires_in":3600}`)
	}))
	defer okServer.Close()
	first := ingestion.NewHHTokenSource(okServer.Client(), okServer.URL, "client", "secret", "agent", "")
	first.SetCachePath(cachePath)
	if token, err := first.Token(context.Background()); err != nil || token != "cached-token" {
		t.Fatalf("first token = %q, %v", token, err)
	}

	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "app token refresh too early", http.StatusForbidden)
	}))
	defer denied.Close()
	second := ingestion.NewHHTokenSource(denied.Client(), denied.URL, "client", "secret", "agent", "")
	second.SetCachePath(cachePath)
	if token, err := second.Token(context.Background()); err != nil || token != "cached-token" {
		t.Fatalf("cached token = %q, %v", token, err)
	}
}

func TestFetchHHApplicationToken(t *testing.T) {
	var gotForm string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q", ct)
		}
		if ua := r.Header.Get("HH-User-Agent"); ua != "test-agent" {
			t.Errorf("hh-user-agent = %q", ua)
		}
		body, _ := io.ReadAll(r.Body)
		gotForm = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"secret-token","token_type":"bearer","expires_in":86400}`))
	}))
	defer server.Close()

	token, err := ingestion.FetchHHApplicationToken(context.Background(), server.Client(), server.URL, "client", "secret", "test-agent")
	if err != nil {
		t.Fatalf("FetchHHApplicationToken: %v", err)
	}
	if token.AccessToken != "secret-token" || token.TokenType != "bearer" || token.ExpiresIn != 86400 {
		t.Fatalf("token = %+v", token)
	}
	for _, want := range []string{"grant_type=client_credentials", "client_id=client", "client_secret=secret"} {
		if !strings.Contains(gotForm, want) {
			t.Fatalf("form = %q, missing %q", gotForm, want)
		}
	}
}

func TestFetchHHApplicationTokenRejectsBadInput(t *testing.T) {
	if _, err := ingestion.FetchHHApplicationToken(context.Background(), nil, "http://example.test", "", "secret", "test-agent"); err == nil {
		t.Fatal("empty client id: want error")
	}
	if _, err := ingestion.FetchHHApplicationToken(context.Background(), nil, "http://example.test", "client", "", "test-agent"); err == nil {
		t.Fatal("empty client secret: want error")
	}
	if _, err := ingestion.FetchHHApplicationToken(context.Background(), nil, "ftp://example.test", "client", "secret", "test-agent"); err == nil {
		t.Fatal("unsupported scheme: want error")
	}
	if _, err := ingestion.FetchHHApplicationToken(nil, nil, "http://example.test", "client", "secret", "test-agent"); err == nil {
		t.Fatal("nil context: want error")
	}
}

func TestFetchHHApplicationTokenErrorsAreSafe(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"invalid_client"}`},
		{name: "empty token", status: http.StatusOK, body: `{}`},
		{name: "invalid json", status: http.StatusOK, body: `not-json`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := ingestion.FetchHHApplicationToken(context.Background(), server.Client(), server.URL, "client", "super-secret", "test-agent")
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(err.Error(), "super-secret") {
				t.Fatalf("error leaks client secret: %v", err)
			}
		})
	}
}

func TestFetchHHApplicationTokenTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"x"}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := ingestion.FetchHHApplicationToken(ctx, server.Client(), server.URL, "client", "secret", "test-agent")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}
