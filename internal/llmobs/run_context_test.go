package llmobs

import (
	"context"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
)

// runIDCaptureStore records the request passed to CreateRequest.
type runIDCaptureStore struct {
	created     bool
	lastRequest Request
}

func (s *runIDCaptureStore) CreateRequest(_ context.Context, request Request) (string, error) {
	s.created = true
	s.lastRequest = request
	return "request-1", nil
}

func (s *runIDCaptureStore) SaveResponse(context.Context, Response) error { return nil }

func (s *runIDCaptureStore) SaveUsage(context.Context, Usage) error { return nil }

func (s *runIDCaptureStore) FinishRequest(context.Context, string, string, string, int, time.Time) error {
	return nil
}

type runIDFakeClient struct{}

func (runIDFakeClient) Chat(context.Context, []llm.Message) (string, error) {
	return `{"ok":true}`, nil
}

func TestRunIDContextRoundTrip(t *testing.T) {
	if got := RunIDFromContext(context.Background()); got != "" {
		t.Fatalf("empty context run id = %q, want empty", got)
	}
	if got := RunIDFromContext(nil); got != "" {
		t.Fatalf("nil context run id = %q, want empty", got)
	}

	ctx := WithRunID(context.Background(), "  run-123  ")
	if got := RunIDFromContext(ctx); got != "run-123" {
		t.Fatalf("run id = %q, want trimmed run-123", got)
	}
	if got := RunIDFromContext(WithRunID(ctx, "run-456")); got != "run-456" {
		t.Fatalf("nested run id = %q, want run-456", got)
	}

	// An empty run id must not change the context: calls outside a run keep
	// run_id NULL in the audit table.
	base := context.Background()
	if got := WithRunID(base, ""); got != base {
		t.Fatal("empty run id must return the original context")
	}
	if got := WithRunID(base, "   "); got != base {
		t.Fatal("whitespace run id must return the original context")
	}
	if got := WithRunID(nil, "run-1"); got != nil {
		t.Fatal("nil context must stay nil")
	}
}

func TestRecorderAttachesRunIDFromContext(t *testing.T) {
	store := &runIDCaptureStore{}
	recorder := NewRecorder(runIDFakeClient{}, store, "analyst", "provider", "model")

	ctx := WithRunID(context.Background(), "run-42")
	if _, err := recorder.Chat(ctx, []llm.Message{{Role: llm.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !store.created {
		t.Fatal("request was not recorded")
	}
	if store.lastRequest.RunID != "run-42" {
		t.Fatalf("recorded run_id = %q, want run-42", store.lastRequest.RunID)
	}
}

func TestRecorderWithoutRunIDKeepsEmptyRunID(t *testing.T) {
	store := &runIDCaptureStore{}
	recorder := NewRecorder(runIDFakeClient{}, store, "interviewer", "provider", "model")

	if _, err := recorder.Chat(context.Background(), nil); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !store.created {
		t.Fatal("request was not recorded")
	}
	if store.lastRequest.RunID != "" {
		t.Fatalf("recorded run_id = %q, want empty for a call outside a run", store.lastRequest.RunID)
	}
}
