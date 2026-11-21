package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

// retryChatStub implements chatStarter and the optional retryChat capability. It
// can hold RetryFinalize open, which is what the double-start guard is checked
// against.
type retryChatStub struct {
	mu         sync.Mutex
	checkErr   error
	retryErr   error
	calls      int
	checkCalls int
	started    chan struct{}
	release    chan struct{}
	done       chan struct{}
	doneOnce   sync.Once
}

func (s *retryChatStub) Start(string, string) error { return nil }

func (s *retryChatStub) Messages(string) ([]chat.Message, error) { return nil, chat.ErrNotFound }

func (s *retryChatStub) Post(string, string) (chat.Message, error) { return chat.Message{}, nil }

func (s *retryChatStub) Subscribe(string) (<-chan chat.Event, func(), error) {
	return nil, func() {}, nil
}

func (s *retryChatStub) CheckRetryFinalize(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkCalls++
	return s.checkErr
}

func (s *retryChatStub) RetryFinalize(context.Context, string) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.release != nil {
		<-s.release
	}
	if s.done != nil {
		s.doneOnce.Do(func() { close(s.done) })
	}
	return s.retryErr
}

func (s *retryChatStub) retryCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *retryChatStub) retryChecks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkCalls
}

func (s *retryChatStub) setCheckErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkErr = err
}

// waitForRetryRetries polls until the background retry has been invoked, so the
// test does not depend on goroutine scheduling.
func waitForRetryRetries(t *testing.T, stub *retryChatStub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stub.retryCalls() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("retry was called %d times, want %d", stub.retryCalls(), want)
}

func waitForRetryIdle(t *testing.T, server *Server, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !server.retryLocks.isActive(runID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("retry guard for %s was not released", runID)
}

func retryStore() *memStore {
	store := newMemStore()
	store.seedRun(domain.Run{ID: "run-1", Status: domain.RunFailed, Profession: "cook"})
	return store
}

func TestRetryRunStartsBackgroundFinalization(t *testing.T) {
	store := retryStore()
	stub := &retryChatStub{}
	handler := NewServerWithChat(store, stub).Routes()

	rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	var body retryRunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.RunID != "run-1" || body.Status != string(domain.RunRunning) {
		t.Fatalf("body = %+v, want run-1/running", body)
	}
	if got := stub.retryChecks(); got != 1 {
		t.Fatalf("CheckRetryFinalize calls = %d, want 1", got)
	}
	waitForRetryRetries(t, stub, 1)
}

func TestRetryRunUnknownRunIsNotFound(t *testing.T) {
	stub := &retryChatStub{}
	handler := NewServerWithChat(newMemStore(), stub).Routes()

	rec := do(t, handler, http.MethodPost, "/api/v1/runs/missing/retry", "user", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "NOT_FOUND" {
		t.Fatalf("code = %q, want NOT_FOUND", code)
	}
	if got := stub.retryCalls(); got != 0 {
		t.Fatalf("retry was called %d times for an unknown run", got)
	}
}

func TestRetryRunRoles(t *testing.T) {
	for _, role := range []string{"admin", "user", "specialist"} {
		t.Run(role, func(t *testing.T) {
			store := retryStore()
			stub := &retryChatStub{}
			handler := NewServerWithChat(store, stub).Routes()
			rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", role, "")
			if rec.Code != http.StatusAccepted {
				t.Fatalf("role %s: status = %d, want 202 (%s)", role, rec.Code, rec.Body.String())
			}
		})
	}
	t.Run("no token", func(t *testing.T) {
		handler := NewServerWithChat(retryStore(), &retryChatStub{}).Routes()
		rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("not the owner", func(t *testing.T) {
		store := newMemStore()
		store.seedRun(domain.Run{ID: "run-1", Status: domain.RunFailed, Profession: "cook", CreatedBy: "someone-else"})
		handler := NewServerWithChat(store, &retryChatStub{}).Routes()
		rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestRetryRunWithoutChatServiceIsNotImplemented(t *testing.T) {
	// The in-memory demo server has no chat service at all, and a chat service
	// without the retry capability cannot repeat a finalization either.
	cases := map[string]http.Handler{
		"no chat service": NewServer(newMemStore()).Routes(),
		"no retry method": NewServerWithChat(retryStore(), guestChat{}).Routes(),
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "admin", "")
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501 (%s)", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != "NOT_IMPLEMENTED" {
				t.Fatalf("code = %q, want NOT_IMPLEMENTED", code)
			}
		})
	}
}

func TestRetryRunMapsPreconditionErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"tree already exists", chat.ErrTreeAlreadyExists, http.StatusConflict, "TREE_ALREADY_EXISTS"},
		{"nothing to retry", chat.ErrNothingToRetry, http.StatusConflict, "NOTHING_TO_RETRY"},
		{"already running", chat.ErrRetryInProgress, http.StatusConflict, "RETRY_IN_PROGRESS"},
		{"session conflict", chat.ErrVersionConflict, http.StatusConflict, "CONFLICT"},
		{"no finalizer", chat.ErrFinalizerUnavailable, http.StatusNotImplemented, "NOT_IMPLEMENTED"},
		{"unknown run", chat.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"internal", errors.New("database unavailable"), http.StatusInternalServerError, "INTERNAL"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := retryStore()
			stub := &retryChatStub{checkErr: testCase.err}
			server := NewServerWithChat(store, stub)
			handler := server.Routes()

			rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
			if rec.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, testCase.wantStatus, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != testCase.wantCode {
				t.Fatalf("code = %q, want %q", code, testCase.wantCode)
			}
			if got := stub.retryCalls(); got != 0 {
				t.Fatalf("retry was called %d times after a rejected precondition", got)
			}
			// A rejected request must release the guard immediately.
			if server.retryLocks.isActive("run-1") {
				t.Fatal("retry guard was not released after a precondition error")
			}
		})
	}
}

func TestRetryRunAcceptsWhenLLMIsUnavailable(t *testing.T) {
	// An unavailable LLM must not turn the retry into an error response: the
	// background run fails again and stays retryable once the provider is back.
	store := retryStore()
	stub := &retryChatStub{retryErr: chat.ErrAnalysisUnavailable}
	handler := NewServerWithChat(store, stub).Routes()

	rec := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	waitForRetryRetries(t, stub, 1)
}

func TestRetryRunRejectsDoubleStart(t *testing.T) {
	store := retryStore()
	stub := &retryChatStub{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	server := NewServerWithChat(store, stub)
	handler := server.Routes()

	first := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202 (%s)", first.Code, first.Body.String())
	}
	<-stub.started

	second := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409 (%s)", second.Code, second.Body.String())
	}
	if code := decodeErrorCode(t, second.Body.Bytes()); code != "RETRY_IN_PROGRESS" {
		t.Fatalf("second code = %q, want RETRY_IN_PROGRESS", code)
	}
	// The second request must be rejected before any precondition work.
	if got := stub.retryChecks(); got != 1 {
		t.Fatalf("CheckRetryFinalize calls = %d, want 1", got)
	}

	close(stub.release)
	<-stub.done
	waitForRetryIdle(t, server, "run-1")

	// Once the background retry is over, the run may be retried again (the real
	// service then answers TREE_ALREADY_EXISTS if the tree was saved).
	third := do(t, handler, http.MethodPost, "/api/v1/runs/run-1/retry", "user", "")
	if third.Code != http.StatusAccepted {
		t.Fatalf("third status = %d, want 202 (%s)", third.Code, third.Body.String())
	}
	waitForRetryRetries(t, stub, 2)
}
