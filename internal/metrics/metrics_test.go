package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTextExposition(t *testing.T) {
	m := New()
	m.ObserveHTTPRequest(http.MethodGet, "/health", http.StatusOK, 50*time.Millisecond)
	m.ObserveHTTPRequest(http.MethodGet, "/health", http.StatusOK, 2*time.Second)
	m.ObserveHTTPRequest(http.MethodGet, "/missing", http.StatusNotFound, 5*time.Millisecond)
	m.ObserveLLMRequest("extractor", "openrouter/mistral", "ok", 3*time.Second)
	m.AddTokens("extractor", "openrouter/mistral", 1200, 340)
	m.ObserveAgentStep("extractor", "extract.nodes", "ok")
	m.ObserveAgentStep("extractor", "extract.nodes", "retry")
	m.ObserveRun("done", 42*time.Second)

	text := m.Text()

	for _, want := range []string{
		"# HELP activity_descriptor_http_requests_total",
		"# TYPE activity_descriptor_http_requests_total counter",
		"# TYPE activity_descriptor_http_request_duration_seconds histogram",
		"# TYPE activity_descriptor_llm_requests_total counter",
		"# TYPE activity_descriptor_llm_tokens_total counter",
		"# TYPE activity_descriptor_agent_steps_total counter",
		"# TYPE activity_descriptor_runs_total counter",
		"# TYPE activity_descriptor_run_duration_seconds histogram",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("exposition missing %q:\n%s", want, text)
		}
	}

	for _, want := range []string{
		`activity_descriptor_http_requests_total{method="GET",route="/health",status="200"} 2`,
		`activity_descriptor_http_requests_total{method="GET",route="/missing",status="404"} 1`,
		`activity_descriptor_llm_requests_total{agent="extractor",model="openrouter/mistral",status="ok"} 1`,
		`activity_descriptor_llm_tokens_total{agent="extractor",model="openrouter/mistral",direction="input"} 1200`,
		`activity_descriptor_llm_tokens_total{agent="extractor",model="openrouter/mistral",direction="output"} 340`,
		`activity_descriptor_agent_steps_total{agent="extractor",step="extract.nodes",status="ok"} 1`,
		`activity_descriptor_agent_steps_total{agent="extractor",step="extract.nodes",status="retry"} 1`,
		`activity_descriptor_runs_total{status="done"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("exposition missing sample %q:\n%s", want, text)
		}
	}

	for _, want := range []string{
		`activity_descriptor_http_request_duration_seconds_bucket{method="GET",route="/health",le="0.025"} 0`,
		`activity_descriptor_http_request_duration_seconds_bucket{method="GET",route="/health",le="0.05"} 1`,
		`activity_descriptor_http_request_duration_seconds_bucket{method="GET",route="/health",le="2.5"} 2`,
		`activity_descriptor_http_request_duration_seconds_bucket{method="GET",route="/health",le="+Inf"} 2`,
		`activity_descriptor_http_request_duration_seconds_count{method="GET",route="/health"} 2`,
		`activity_descriptor_run_duration_seconds_bucket{status="done",le="+Inf"} 1`,
		`activity_descriptor_run_duration_seconds_count{status="done"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("exposition missing histogram sample %q:\n%s", want, text)
		}
	}

	for _, forbidden := range []string{"run_id", "request_id", "prompt", "source_text"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("exposition must not contain %q:\n%s", forbidden, text)
		}
	}
}

func TestTextOmitsUnusedFamilies(t *testing.T) {
	text := New().Text()
	if text != "" {
		t.Fatalf("empty registry exposition = %q, want empty", text)
	}
}

func TestHandler(t *testing.T) {
	m := New()
	m.ObserveAgentStep("validator", "validate", "ok")
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL) //nolint:noctx // test-only request to a local test server
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
		t.Fatalf("Content-Type = %q, want text/plain version 0.0.4", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "activity_descriptor_agent_steps_total") {
		t.Fatalf("body missing metric: %s", body)
	}
}

func TestMiddlewareRecordsRouteAndGauge(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := m.Middleware(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}

	// Unmatched paths are grouped as "unmatched", so arbitrary 404 paths
	// cannot explode label cardinality.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))

	text := m.Text()
	for _, want := range []string{
		`activity_descriptor_http_requests_total{method="GET",route="/health",status="200"} 1`,
		`activity_descriptor_http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`activity_descriptor_http_in_flight_requests 0`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing sample %q:\n%s", want, text)
		}
	}
}

func TestNormalizePathHidesIDs(t *testing.T) {
	cases := map[string]string{
		"/api/v1/runs/run-7/messages":                            "/api/v1/runs/{id}/messages",
		"/api/v1/runs/3f0c2a8e-1b1a-4a6b-9a2a-1f0e2d3c4b5a/tree": "/api/v1/runs/{id}/tree",
		"/health":               "/health",
		"/metrics":              "/metrics",
		"/does-not-exist":       "unmatched",
		"/api/v1/unknown/run-1": "unmatched",
	}
	for input, want := range cases {
		if got := normalizePath(input); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", input, got, want)
		}
	}
	if strings.Contains(normalizePath("/api/v1/runs/run-7"), "run-7") {
		t.Fatal("run identifier leaked into a normalized path")
	}
}

func TestLabelEscaping(t *testing.T) {
	m := New()
	m.AddTokens(`agent"quoted\`, "model\nx", 1, 0)
	text := m.Text()
	want := `agent="agent\"quoted\\",model="model\nx",direction="input"} 1`
	if !strings.Contains(text, want) {
		t.Fatalf("escaped labels missing %q:\n%s", want, text)
	}
}

func TestConcurrentUse(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				m.ObserveHTTPRequest(http.MethodGet, "/health", http.StatusOK, time.Millisecond)
				m.ObserveLLMRequest("extractor", "m", "ok", time.Millisecond)
				m.AddTokens("extractor", "m", 1, 2)
				m.ObserveAgentStep("extractor", "step", "ok")
				m.ObserveRun("done", time.Second)
				_ = m.Text()
			}
		}()
	}
	wg.Wait()
	if !strings.Contains(m.Text(), `activity_descriptor_http_requests_total{method="GET",route="/health",status="200"} 800`) {
		t.Fatalf("unexpected counter total:\n%s", m.Text())
	}
}
