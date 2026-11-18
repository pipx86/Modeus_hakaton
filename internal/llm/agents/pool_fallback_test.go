package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
)

const validInterviewerAnswer = `{"question":{"id":"q1","level":"role","text":"What do you do?"},"rationale":"r","is_clarification":false}`

// fakeProvider is an OpenAI-compatible test provider that records requested
// models and replies per model. It never touches the network.
type fakeProvider struct {
	server    *httptest.Server
	responses map[string]providerResponse
	fallback  providerResponse

	mu     sync.Mutex
	models []string
}

type providerResponse struct {
	status int
	body   string
	delay  time.Duration
}

func newFakeProvider(t *testing.T, responses map[string]providerResponse) *fakeProvider {
	t.Helper()
	provider := &fakeProvider{responses: responses, fallback: providerResponse{status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)}}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.handle))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *fakeProvider) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var request struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &request)

	p.mu.Lock()
	p.models = append(p.models, request.Model)
	p.mu.Unlock()

	response, ok := p.responses[request.Model]
	if !ok {
		response = p.fallback
	}
	if response.delay > 0 {
		time.Sleep(response.delay)
	}
	if response.status != 0 && response.status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream failure","code":503}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, response.body)
}

func (p *fakeProvider) requestedModels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.models...)
}

func chatCompletion(content string) string {
	payload := map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"content": content}}},
		"usage":   map[string]int{"prompt_tokens": 3, "completion_tokens": 5, "total_tokens": 8},
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func testSpec(model, fallbackModel string, timeout time.Duration) Spec {
	return Spec{
		Model:           model,
		FallbackModel:   fallbackModel,
		Temperature:     0.2,
		MaxTokens:       200,
		ReasoningEffort: "low",
		Timeout:         timeout,
	}
}

func testPoolConfig(t *testing.T, baseURL string, agents map[AgentName]Spec) Config {
	t.Helper()
	return Config{
		BaseURL:           baseURL,
		APIKey:            "test-key",
		DefaultModel:      "primary-model",
		Timeout:           2 * time.Second,
		MaxConcurrency:    2,
		RequestsPerMinute: 1000,
		RetryMax:          0,
		Agents:            agents,
	}
}

func benignMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "You are a helpful interviewer."},
		{Role: llm.RoleUser, Content: "hello"},
	}
}

func TestPoolPrimarySuccessDoesNotCallFallback(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	answer, err := client.Chat(context.Background(), benignMessages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validInterviewerAnswer {
		t.Fatalf("answer = %q", answer)
	}
	if models := provider.requestedModels(); len(models) != 1 || models[0] != "primary-model" {
		t.Fatalf("requested models = %v, want [primary-model]", models)
	}
}

func TestPoolFallsBackOnUnavailableAndRecordsTruthfulMetrics(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusServiceUnavailable},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	answer, err := client.Chat(context.Background(), benignMessages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validInterviewerAnswer {
		t.Fatalf("answer = %q", answer)
	}
	models := provider.requestedModels()
	if len(models) != 2 || models[0] != "primary-model" || models[1] != "backup-model" {
		t.Fatalf("requested models = %v, want [primary-model backup-model]", models)
	}

	var metrics strings.Builder
	if err := pool.Metrics().WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	for _, want := range []string{
		`agent="interviewer",model="primary-model",status="error"`,
		`agent="interviewer",model="backup-model",status="ok"`,
		`agent_tokens_total{agent="interviewer",model="backup-model",direction="output"}`,
	} {
		if !strings.Contains(metrics.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics.String())
		}
	}
}

func TestPoolFallsBackOnTimeout(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer), delay: 400 * time.Millisecond},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 60*time.Millisecond),
		Analyst:     testSpec("primary-model", "", 60*time.Millisecond),
		Synthesizer: testSpec("primary-model", "", 60*time.Millisecond),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	answer, err := client.Chat(context.Background(), benignMessages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validInterviewerAnswer {
		t.Fatalf("answer = %q", answer)
	}
	models := provider.requestedModels()
	if len(models) != 2 || models[0] != "primary-model" || models[1] != "backup-model" {
		t.Fatalf("requested models = %v, want [primary-model backup-model]", models)
	}
}

func TestPoolNoFallbackConfiguredKeepsPrimaryOnly(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, ok := client.(*llm.FallbackClient); ok {
		t.Fatal("pool wrapped a client in a fallback chain although no fallback model is configured")
	}
	if _, err := client.Chat(context.Background(), benignMessages()); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if models := provider.requestedModels(); len(models) != 1 {
		t.Fatalf("requested models = %v, want exactly one primary call", models)
	}
}

func TestPoolNoFallbackDoesNotCallAnotherModelOnFailure(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusServiceUnavailable},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, err := client.Chat(context.Background(), benignMessages()); err == nil {
		t.Fatal("expected the primary failure to be returned")
	}
	if models := provider.requestedModels(); len(models) != 1 || models[0] != "primary-model" {
		t.Fatalf("requested models = %v, want only the primary call", models)
	}
}

func TestPoolNonRetryableFailureDoesNotFallback(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusBadRequest},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Chat(context.Background(), benignMessages())
	if err == nil {
		t.Fatal("expected a 400 error")
	}
	if llm.Classify(err) != llm.ErrorInvalidResponse {
		t.Fatalf("classify = %s, want invalid_response", llm.Classify(err))
	}
	if models := provider.requestedModels(); len(models) != 1 || models[0] != "primary-model" {
		t.Fatalf("requested models = %v, want no fallback call for a 4xx", models)
	}
}

func TestPoolSchemaInvalidAnswerUsesFallback(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusOK, body: chatCompletion("not-json")},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	answer, err := client.Chat(context.Background(), benignMessages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validInterviewerAnswer {
		t.Fatalf("answer = %q, want the schema-valid backup answer", answer)
	}
	models := provider.requestedModels()
	if len(models) != 2 || models[1] != "backup-model" {
		t.Fatalf("requested models = %v, want a schema-driven fallback", models)
	}
}

func TestPoolDuplicateFallbackModelBuildsNoChain(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "primary-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, ok := client.(*llm.FallbackClient); ok {
		t.Fatal("a fallback equal to the primary model must not build a fallback chain")
	}
	if _, err := client.Chat(context.Background(), benignMessages()); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if models := provider.requestedModels(); len(models) != 1 {
		t.Fatalf("requested models = %v, want a single call", models)
	}
}

func TestPoolDecoratorReceivesTruthfulModels(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusServiceUnavailable},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})

	type decoration struct {
		agent AgentName
		model string
	}
	var mu sync.Mutex
	var decorated []decoration
	pool, err := NewPool(cfg, WithClientDecorator(func(name AgentName, model string, client llm.ChatClient) llm.ChatClient {
		mu.Lock()
		decorated = append(decorated, decoration{agent: name, model: model})
		mu.Unlock()
		return client
	}))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, err := client.Chat(context.Background(), benignMessages()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var interviewerModels []string
	for _, item := range decorated {
		if item.agent == Interviewer {
			interviewerModels = append(interviewerModels, item.model)
		}
	}
	if len(interviewerModels) != 2 || interviewerModels[0] != "primary-model" || interviewerModels[1] != "backup-model" {
		t.Fatalf("decorated interviewer models = %v, want [primary-model backup-model] so audit rows stay truthful", interviewerModels)
	}
}

func TestPoolRejectsCorruptFallbackSpec(t *testing.T) {
	cfg := testPoolConfig(t, "http://127.0.0.1:0", map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", time.Second),
		Analyst:     testSpec("primary-model", "", time.Second),
		Synthesizer: testSpec("primary-model", "", time.Second),
	})
	// The provider client itself validates the model, so an empty primary model
	// must surface as a build error instead of a silent fallback-only pool.
	cfg.Agents[Interviewer] = testSpec("", "backup-model", time.Second)
	if _, err := NewPool(cfg); err == nil {
		t.Fatal("expected an error for an empty primary model")
	}
}

func TestPoolFallsBackOnEmptyPrimaryAnswer(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusOK, body: chatCompletion("")},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	answer, err := client.Chat(context.Background(), benignMessages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validInterviewerAnswer {
		t.Fatalf("answer = %q", answer)
	}
}

// Ensure the package-level helper does not turn provider errors into something
// unclassified: fallback callers rely on llm.Classify.
func TestPoolFallbackErrorClassification(t *testing.T) {
	provider := newFakeProvider(t, map[string]providerResponse{
		"primary-model": {status: http.StatusUnauthorized},
		"backup-model":  {status: http.StatusOK, body: chatCompletion(validInterviewerAnswer)},
	})
	cfg := testPoolConfig(t, provider.server.URL, map[AgentName]Spec{
		Interviewer: testSpec("primary-model", "backup-model", 2*time.Second),
		Analyst:     testSpec("primary-model", "", 2*time.Second),
		Synthesizer: testSpec("primary-model", "", 2*time.Second),
	})
	pool, err := NewPool(cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	client, err := pool.Client(Interviewer)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	_, err = client.Chat(context.Background(), benignMessages())
	if err == nil {
		t.Fatal("expected an auth error")
	}
	if llm.Classify(err) != llm.ErrorAuth {
		t.Fatalf("classify = %s, want auth", llm.Classify(err))
	}
	var degraded *llm.DegradedError
	if !errors.As(err, &degraded) {
		t.Fatalf("err = %v, want a *llm.DegradedError so the run can be marked degraded", err)
	}
	if models := provider.requestedModels(); len(models) != 1 {
		t.Fatalf("requested models = %v, want no fallback for auth", models)
	}
}
