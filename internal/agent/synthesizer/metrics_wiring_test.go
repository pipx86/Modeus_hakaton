package synthesizer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/llm/agents"
)

// TestSynthesizerPoolClientRecordsAgentMetric wires the real agent pool in front
// of an OpenAI-compatible test provider and proves the end-to-end guarantee the
// admin agent table depends on: one synthesizer normalization call is recorded
// as activity_descriptor_agent_calls_total{agent="synthesizer"} together with
// its token usage, and the returned title is actually applied.
func TestSynthesizerPoolClientRecordsAgentMetric(t *testing.T) {
	const model = "synthesizer-normalizer-model"
	const normalizedTitle = "Писать SQL-запросы и выгружать данные"
	normalizedAnswer := `{"nodes":[{"id":"tax-task-sql","title":"` + normalizedTitle + `"}]}`

	var mu sync.Mutex
	var requestedModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requestedModels = append(requestedModels, request.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": normalizedAnswer}}},
			"usage":   map[string]int{"prompt_tokens": 21, "completion_tokens": 9, "total_tokens": 30},
		})
	}))
	t.Cleanup(server.Close)

	spec := agents.Spec{
		Model:           model,
		Temperature:     0.1,
		MaxTokens:       512,
		ReasoningEffort: "low",
		Timeout:         5 * time.Second,
	}
	cfg := agents.Config{
		BaseURL:           server.URL,
		APIKey:            "test-key",
		DefaultModel:      model,
		Timeout:           5 * time.Second,
		MaxConcurrency:    2,
		RequestsPerMinute: 1000,
		RetryMax:          0,
		Agents: map[agents.AgentName]agents.Spec{
			agents.Interviewer: spec,
			agents.Analyst:     spec,
			agents.Synthesizer: spec,
		},
	}
	pool, err := agents.NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(agents.Synthesizer)
	if err != nil {
		t.Fatalf("pool.Client(synthesizer): %v", err)
	}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	in := loadConflictInput(t)
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if got := nodeIndex(out.Tree)["tax-task-sql"].Title; got != normalizedTitle {
		t.Fatalf("merged title = %q, want the normalized title %q", got, normalizedTitle)
	}

	mu.Lock()
	models := append([]string(nil), requestedModels...)
	mu.Unlock()
	if len(models) != 1 || models[0] != model {
		t.Fatalf("provider requests = %v, want exactly one call to %q", models, model)
	}

	var metrics strings.Builder
	if err := pool.Metrics().WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	for _, want := range []string{
		`activity_descriptor_agent_calls_total{agent="synthesizer",model="` + model + `",status="ok",code="ok"} 1`,
		`activity_descriptor_agent_tokens_total{agent="synthesizer",model="` + model + `",direction="input"} 21`,
		`activity_descriptor_agent_tokens_total{agent="synthesizer",model="` + model + `",direction="output"} 9`,
	} {
		if !strings.Contains(metrics.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics.String())
		}
	}
}
