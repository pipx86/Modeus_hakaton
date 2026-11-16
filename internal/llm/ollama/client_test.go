package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
)

const testModel = "test-model"

func successJSON() string {
	return `{"message":{"role":"assistant","content":"hello"},"done":true}`
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestClient(t *testing.T, url string, timeout time.Duration, logger *slog.Logger) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: url, Model: testModel, Timeout: timeout}, WithLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func userMessage(content string) []llm.Message {
	return []llm.Message{{Role: llm.RoleUser, Content: content}}
}

func TestChatSuccess(t *testing.T) {
	requests := make(chan chatRequest, 1)
	paths := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- req
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, successJSON())
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, time.Second, discardLogger())
	got, err := client.Chat(context.Background(), userMessage("hi"))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "hello" {
		t.Fatalf("Chat = %q, want %q", got, "hello")
	}

	if path := <-paths; path != chatPath {
		t.Fatalf("path = %q, want %q", path, chatPath)
	}
	req := <-requests
	if req.Model != testModel {
		t.Fatalf("model = %q, want %q", req.Model, testModel)
	}
	if req.Stream {
		t.Fatal("stream must be false")
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
		t.Fatalf("messages = %#v", req.Messages)
	}
}

func TestChatUpstream500DoesNotKillProcess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"boom"}`)
			return
		}
		_, _ = io.WriteString(w, successJSON())
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, time.Second, discardLogger())
	_, err := client.Chat(context.Background(), userMessage("first"))
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("err = %v, want *UpstreamError", err)
	}
	if upstream.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d", upstream.StatusCode)
	}

	got, err := client.Chat(context.Background(), userMessage("second"))
	if err != nil {
		t.Fatalf("second Chat after 500: %v", err)
	}
	if got != "hello" {
		t.Fatalf("second Chat = %q", got)
	}
}

func TestChatTimeoutDoesNotKillProcess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			time.Sleep(200 * time.Millisecond)
		}
		_, _ = io.WriteString(w, successJSON())
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, 50*time.Millisecond, discardLogger())
	_, err := client.Chat(context.Background(), userMessage("slow"))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}

	got, err := client.Chat(context.Background(), userMessage("fast"))
	if err != nil {
		t.Fatalf("second Chat after timeout: %v", err)
	}
	if got != "hello" {
		t.Fatalf("second Chat = %q", got)
	}
}

func TestChatContextCancelled(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1", time.Second, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Chat(ctx, userMessage("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestChatMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not-json")
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, time.Second, discardLogger())
	_, err := client.Chat(context.Background(), userMessage("x"))
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("err = %v, want ErrDecode", err)
	}
}

func TestChatLogsErrorWithoutPrompt(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, time.Second, logger)
	_, _ = client.Chat(context.Background(), userMessage("secret-prompt"))

	logged := buf.String()
	if !strings.Contains(logged, "ollama chat failed") {
		t.Fatalf("log does not contain failure message: %q", logged)
	}
	if !strings.Contains(logged, "status=500") {
		t.Fatalf("log does not contain status: %q", logged)
	}
	if strings.Contains(logged, "secret-prompt") {
		t.Fatalf("log leaked prompt content: %q", logged)
	}
}

func TestChatRejectsEmptyMessages(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1", time.Second, discardLogger())
	if _, err := client.Chat(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestChatRejectsNilContext(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:1", time.Second, discardLogger())
	//lint:ignore SA1012 nil context is the behaviour under test
	if _, err := client.Chat(nil, userMessage("x")); err == nil {
		t.Fatal("expected error for nil context")
	}
}

func TestChatBodyTimeoutIsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, successJSON())
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, 50*time.Millisecond, discardLogger())
	_, err := client.Chat(context.Background(), userMessage("slow body"))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if errors.Is(err, ErrDecode) {
		t.Fatalf("err = %v, must not be ErrDecode", err)
	}
}

func TestChatReportsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"ok"},"prompt_eval_count":9,"eval_count":4,"done":true}`)
	}))
	t.Cleanup(srv.Close)

	usageCh := make(chan llm.Usage, 1)
	client, err := New(Config{BaseURL: srv.URL, Model: testModel, Timeout: time.Second},
		WithLogger(discardLogger()), WithUsageObserver(func(usage llm.Usage) { usageCh <- usage }))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Chat(context.Background(), userMessage("hi")); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	select {
	case usage := <-usageCh:
		if usage.PromptTokens != 9 || usage.CompletionTokens != 4 || usage.TotalTokens != 13 {
			t.Fatalf("usage = %+v", usage)
		}
	case <-time.After(time.Second):
		t.Fatal("usage observer was not called")
	}
}
