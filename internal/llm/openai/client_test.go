package openai

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

const testKey = "test-key"

func successJSON() string {
	return `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestClient(t *testing.T, url string, timeout time.Duration, logger *slog.Logger) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL:     url,
		APIKey:      testKey,
		Model:       "qwen/qwen3-32b",
		Timeout:     timeout,
		HTTPReferer: "https://example.test",
		Title:       "test-app",
	}, WithLogger(logger))
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
	auths := make(chan string, 1)
	paths := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		auths <- r.Header.Get("Authorization")
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
	if auth := <-auths; auth != "Bearer "+testKey {
		t.Fatalf("auth = %q", auth)
	}
	req := <-requests
	if req.Model != "qwen/qwen3-32b" {
		t.Fatalf("model = %q", req.Model)
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
			_, _ = io.WriteString(w, `{"error":{"message":"boom","code":500}}`)
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
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, time.Second, logger)
	_, _ = client.Chat(context.Background(), userMessage("secret-prompt"))

	logged := buf.String()
	if !strings.Contains(logged, "openai chat failed") {
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

func TestChatSendsRequestOptions(t *testing.T) {
	requests := make(chan chatRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- req
		_, _ = io.WriteString(w, successJSON())
	}))
	t.Cleanup(srv.Close)

	temperature := 0.3
	client, err := New(Config{
		BaseURL:         srv.URL,
		APIKey:          testKey,
		Model:           "m",
		Timeout:         time.Second,
		MaxTokens:       321,
		Temperature:     &temperature,
		ReasoningEffort: "low",
		ResponseFormat:  json.RawMessage(`{"type":"json_object"}`),
	}, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Chat(context.Background(), userMessage("hi")); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	req := <-requests
	if req.MaxTokens != 321 {
		t.Fatalf("max_tokens = %d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.3 {
		t.Fatalf("temperature = %v", req.Temperature)
	}
	if req.ReasoningEffort != "low" {
		t.Fatalf("reasoning_effort = %q", req.ReasoningEffort)
	}
	if string(req.ResponseFormat) != `{"type":"json_object"}` {
		t.Fatalf("response_format = %s", req.ResponseFormat)
	}
}

func TestNewRejectsUnknownReasoningEffort(t *testing.T) {
	if _, err := New(Config{APIKey: testKey, Model: "m", ReasoningEffort: "turbo"}); err == nil {
		t.Fatal("expected error for unknown reasoning effort")
	}
}
