package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/metrics"
)

func testHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newDemoHandler(metrics.New(), logger)
}

func doRequest(t *testing.T, handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHealthEndpoint(t *testing.T) {
	rec := doRequest(t, testHandler(), http.MethodGet, "/health", "", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body.Status != "ok" || body.Mode != "demo" || body.Store != "in-memory" {
		t.Fatalf("unexpected health body: %+v", body)
	}
	if body.Time.IsZero() {
		t.Fatal("health time must be set")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	handler := testHandler()
	doRequest(t, handler, http.MethodGet, "/health", "", "")
	doRequest(t, handler, http.MethodGet, "/health", "", "")

	rec := doRequest(t, handler, http.MethodGet, "/metrics", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `activity_descriptor_http_requests_total{method="GET",route="/health",status="200"} 2`) {
		t.Fatalf("metrics missing health samples:\n%s", body)
	}
	if !strings.Contains(body, "activity_descriptor_http_in_flight_requests") {
		t.Fatalf("metrics missing in-flight gauge:\n%s", body)
	}
	if strings.Contains(body, "run_id") {
		t.Fatalf("metrics must not contain run identifiers:\n%s", body)
	}
}

func TestBackendHealthIsPublic(t *testing.T) {
	rec := doRequest(t, testHandler(), http.MethodGet, "/api/v1/health", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (backend health must stay public)", rec.Code)
	}
}

func TestProtectedEndpointRequiresAuth(t *testing.T) {
	rec := doRequest(t, testHandler(), http.MethodPost, "/api/v1/runs", `{"profession":"analyst"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a token", rec.Code)
	}
}

func TestChatFlowWithJWT(t *testing.T) {
	auth.SetSecretForTests("demo-api-test-secret")
	token, err := auth.Issue("demo-test", auth.RoleAdmin, 5*time.Minute)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	handler := testHandler()

	// Create a run: the backend starts the chat session and asks the first
	// fallback question asynchronously.
	rec := doRequest(t, handler, http.MethodPost, "/api/v1/runs", `{"profession":"Аналитик данных"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create run status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || run.ID == "" {
		t.Fatalf("decode run: %v, body = %s", err, rec.Body.String())
	}

	messagesPath := "/api/v1/runs/" + run.ID + "/messages"
	var messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec = doRequest(t, handler, http.MethodGet, messagesPath, "", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("get messages status = %d, body = %s", rec.Code, rec.Body.String())
		}
		messages = messages[:0]
		if err := json.Unmarshal(rec.Body.Bytes(), &messages); err != nil {
			t.Fatalf("decode messages: %v", err)
		}
		if len(messages) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(messages) == 0 {
		t.Fatal("expected the fallback interviewer to ask a question")
	}
	if messages[0].Role != "agent" || messages[0].Content == "" {
		t.Fatalf("first message = %+v, want a non-empty agent question", messages[0])
	}

	rec = doRequest(t, handler, http.MethodPost, messagesPath, `{"content":"Каждый день готовлю отчётность по продукту."}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post message status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Chat HTTP traffic must be observable through /metrics without run ids.
	rec = doRequest(t, handler, http.MethodGet, "/metrics", "", "")
	body := rec.Body.String()
	if !strings.Contains(body, `route="/api/v1/runs"`) {
		t.Fatalf("metrics missing create-run route label:\n%s", body)
	}
	if !strings.Contains(body, `route="/api/v1/runs/{id}/messages"`) {
		t.Fatalf("metrics missing normalized messages route label:\n%s", body)
	}
	for _, forbidden := range []string{"run_id", run.ID} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics must not contain %q:\n%s", forbidden, body)
		}
	}
}

func TestProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ok.Close)
	if err := probe(ok.URL); err != nil {
		t.Fatalf("probe healthy server: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(bad.Close)
	if err := probe(bad.URL); err == nil {
		t.Fatal("probe unhealthy server: want error")
	}
}

func TestProbeURL(t *testing.T) {
	cases := map[string]string{
		":8080":          "http://127.0.0.1:8080/health",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/health",
		"127.0.0.1:8081": "http://127.0.0.1:8081/health",
	}
	for addr, want := range cases {
		if got := probeURL(addr); got != want {
			t.Errorf("probeURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// Ensure the demo health response is valid JSON without HTML escaping issues.
func TestHealthJSON(t *testing.T) {
	rec := doRequest(t, testHandler(), http.MethodGet, "/health", "", "")
	var buf bytes.Buffer
	if err := json.Compact(&buf, rec.Body.Bytes()); err != nil {
		t.Fatalf("health response is not valid JSON: %v", err)
	}
}
