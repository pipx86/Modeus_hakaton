package ingestion

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

	token, err := FetchHHApplicationToken(context.Background(), server.Client(), server.URL, "client", "secret", "test-agent")
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
	if _, err := FetchHHApplicationToken(context.Background(), nil, "http://example.test", "", "secret", "test-agent"); err == nil {
		t.Fatal("empty client id: want error")
	}
	if _, err := FetchHHApplicationToken(context.Background(), nil, "http://example.test", "client", "", "test-agent"); err == nil {
		t.Fatal("empty client secret: want error")
	}
	if _, err := FetchHHApplicationToken(context.Background(), nil, "ftp://example.test", "client", "secret", "test-agent"); err == nil {
		t.Fatal("unsupported scheme: want error")
	}
	if _, err := FetchHHApplicationToken(nil, nil, "http://example.test", "client", "secret", "test-agent"); err == nil {
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
			_, err := FetchHHApplicationToken(context.Background(), server.Client(), server.URL, "client", "super-secret", "test-agent")
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
	_, err := FetchHHApplicationToken(ctx, server.Client(), server.URL, "client", "secret", "test-agent")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestHHTokenSourceStaticToken(t *testing.T) {
	src := NewHHTokenSource(nil, "", "id", "secret", "agent", "static-token")
	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "static-token" {
		t.Fatalf("got %q", tok)
	}
}

func TestHHTokenSourceCachesAndRefreshes(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		// короткий expires_in, чтобы можно было проверить refresh
		_, _ = w.Write([]byte(`{"access_token":"tok-` + string(rune('A'+calls-1)) + `","token_type":"bearer","expires_in":2}`))
	}))
	defer server.Close()

	src := NewHHTokenSource(server.Client(), server.URL, "client", "secret", "agent", "")
	src.refreshMargin = 0 // для теста обновляем только после реального expires

	tok1, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != "tok-A" {
		t.Fatalf("first = %q", tok1)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// ещё валиден — второй вызов не должен ходить в сеть
	tok2, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok2 != "tok-A" || calls != 1 {
		t.Fatalf("second = %q calls=%d", tok2, calls)
	}

	// ждём истечения
	time.Sleep(2100 * time.Millisecond)
	tok3, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok3 != "tok-B" || calls != 2 {
		t.Fatalf("after expiry = %q calls=%d", tok3, calls)
	}
}

func TestHHTokenSourceKeepsCacheOnRefreshTooEarly(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"good-token","token_type":"bearer","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden","error_description":"app token refresh too early"}`))
	}))
	defer server.Close()

	src := NewHHTokenSource(server.Client(), server.URL, "client", "secret", "agent", "")
	src.refreshMargin = time.Hour // форсируем refresh при втором вызове

	tok1, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != "good-token" {
		t.Fatalf("first = %q", tok1)
	}

	tok2, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("expected cached token on too-early, got err: %v", err)
	}
	if tok2 != "good-token" {
		t.Fatalf("second = %q, want good-token", tok2)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestHHTokenSourceNoCredentials(t *testing.T) {
	src := NewHHTokenSource(nil, "", "", "", "agent", "")
	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		t.Fatalf("want empty token, got %q", tok)
	}
}
