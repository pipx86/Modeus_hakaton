package agents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
	agentmetrics "activity-descriptor/internal/llm/metrics"
	"activity-descriptor/internal/llm/openai"
)

type fakeClient struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
	errs      []error
	answer    string
	delay     time.Duration
}

func (f *fakeClient) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	f.mu.Lock()
	f.calls++
	index := f.calls - 1
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	delay := f.delay
	var err error
	if index < len(f.errs) {
		err = f.errs[index]
	}
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	if err != nil {
		return "", err
	}
	return f.answer, nil
}

func (f *fakeClient) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestRunnerRetries429(t *testing.T) {
	gate, err := NewGate(1, 1000, 2)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	gate.backoff = time.Millisecond
	fake := &fakeClient{
		errs:   []error{&openai.UpstreamError{StatusCode: 429}, nil},
		answer: "ok",
	}
	runner, err := NewRunner(Analyst, fake, gate)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	answer, err := runner.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != "ok" {
		t.Fatalf("answer = %q", answer)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("calls = %d, want 2", fake.CallCount())
	}
}

func TestRunnerDoesNotRetry400(t *testing.T) {
	gate, _ := NewGate(1, 1000, 2)
	fake := &fakeClient{errs: []error{&openai.UpstreamError{StatusCode: 400}}}
	runner, _ := NewRunner(Analyst, fake, gate)
	if _, err := runner.Chat(context.Background(), nil); err == nil {
		t.Fatal("expected an error")
	}
	if fake.CallCount() != 1 {
		t.Fatalf("calls = %d, want 1", fake.CallCount())
	}
}

func TestRunnerRespectsConcurrency(t *testing.T) {
	gate, _ := NewGate(2, 1000, 0)
	fake := &fakeClient{answer: "ok", delay: 20 * time.Millisecond}
	runner, _ := NewRunner(Interviewer, fake, gate)
	jobs := []Job{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}
	results := gate.Parallel(context.Background(), runner, jobs)
	for _, result := range results {
		if result.Err != nil {
			t.Fatalf("job %s: %v", result.Name, result.Err)
		}
	}
	if fake.maxActive > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", fake.maxActive)
	}
}

func TestGateParallelEmpty(t *testing.T) {
	gate, _ := NewGate(1, 1000, 0)
	results := gate.Parallel(context.Background(), nil, nil)
	if len(results) != 0 {
		t.Fatalf("len = %d", len(results))
	}
}

func TestRunnerNilContext(t *testing.T) {
	gate, _ := NewGate(1, 1000, 0)
	runner, _ := NewRunner(Analyst, &fakeClient{}, gate)
	if _, err := runner.Chat(nil, nil); err == nil {
		t.Fatal("expected an error for nil context")
	}
}

func TestRunnerCancelledContext(t *testing.T) {
	gate, _ := NewGate(1, 1000, 0)
	fake := &fakeClient{answer: "ok", delay: time.Second}
	runner, _ := NewRunner(Analyst, fake, gate)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Chat(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestGateSharedAcrossAgents(t *testing.T) {
	gate, _ := NewGate(1, 1000, 0)
	fake := &fakeClient{answer: "ok", delay: 10 * time.Millisecond}

	var wg sync.WaitGroup
	var max int64
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner, _ := NewRunner(Analyst, fake, gate)
			if _, err := runner.Chat(context.Background(), nil); err != nil {
				t.Errorf("Chat: %v", err)
			}
		}()
	}
	wg.Wait()
	max = int64(fake.maxActive)
	if max > 1 {
		t.Fatalf("shared gate max concurrency = %d, want 1", max)
	}
	_ = atomic.LoadInt64(&max)
}

func TestRunnerRecordsMetrics(t *testing.T) {
	gate, _ := NewGate(1, 1000, 0)
	fake := &fakeClient{answer: "ok"}
	registry := agentmetrics.New()
	runner, err := NewRunnerWithMetrics(Analyst, fake, gate, "nemotron", "low", registry)
	if err != nil {
		t.Fatalf("NewRunnerWithMetrics: %v", err)
	}
	if _, err := runner.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var buf strings.Builder
	if err := registry.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	for _, want := range []string{
		`agent="analyst"`,
		`model="nemotron"`,
		`status="ok"`,
		`reasoning="low"`,
		`activity_descriptor_agent_calls_total`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, buf.String())
		}
	}
}
