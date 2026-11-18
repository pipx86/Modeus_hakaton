package llmobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
)

type recorderFakeClient struct {
	answer string
	err    error
}

func (f recorderFakeClient) Chat(context.Context, []llm.Message) (string, error) {
	return f.answer, f.err
}

type recorderFakeStore struct {
	created   int
	responses int
	finished  int
	usage     int
	lastID    string
	lastError string
}

func (s *recorderFakeStore) CreateRequest(context.Context, Request) (string, error) {
	s.created++
	s.lastID = "request-1"
	return s.lastID, nil
}

func (s *recorderFakeStore) SaveResponse(context.Context, Response) error {
	s.responses++
	return nil
}

func (s *recorderFakeStore) SaveUsage(context.Context, Usage) error {
	s.usage++
	return nil
}

func (s *recorderFakeStore) FinishRequest(_ context.Context, _ string, _, errorCode string, _ int, _ time.Time) error {
	s.finished++
	s.lastError = errorCode
	return nil
}

func TestRecorderWritesSuccessfulCall(t *testing.T) {
	store := &recorderFakeStore{}
	recorder := NewRecorder(recorderFakeClient{answer: `{"ok":true}`}, store, "analyst", "openrouter", "model")
	answer, err := recorder.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hello"}})
	if err != nil || answer != `{"ok":true}` {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if store.created != 1 || store.responses != 1 || store.finished != 1 {
		t.Fatalf("audit calls = %#v", store)
	}
	if store.lastError != "" {
		t.Fatalf("error code = %q", store.lastError)
	}
}

func TestRecorderWritesFailedCall(t *testing.T) {
	store := &recorderFakeStore{}
	recorder := NewRecorder(recorderFakeClient{err: errors.New("provider down")}, store, "interviewer", "openrouter", "model")
	if _, err := recorder.Chat(context.Background(), nil); err == nil {
		t.Fatal("expected provider error")
	}
	if store.finished != 1 || store.responses != 0 {
		t.Fatalf("audit calls = %#v", store)
	}
	if store.lastError == "" {
		t.Fatal("error code was not recorded")
	}
}

func TestRecorderWithoutClient(t *testing.T) {
	if _, err := (&Recorder{}).Chat(context.Background(), nil); err == nil {
		t.Fatal("expected configuration error")
	}
}

type recorderUsageClient struct{}

func (recorderUsageClient) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	llm.ReportUsage(ctx, llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	return `{"ok":true}`, nil
}

func TestRecorderSavesUsageFromContext(t *testing.T) {
	store := &recorderFakeStore{}
	recorder := NewRecorder(recorderUsageClient{}, store, "analyst", "provider", "model")

	if _, err := recorder.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if store.usage != 1 {
		t.Fatalf("usage rows = %d, want 1", store.usage)
	}
}
