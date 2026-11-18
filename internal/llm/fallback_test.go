package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedClient is a ChatClient with scripted answer/error and a call counter.
type scriptedClient struct {
	mu     sync.Mutex
	calls  int
	answer string
	err    error
	delay  time.Duration
}

func (c *scriptedClient) Chat(ctx context.Context, _ []Message) (string, error) {
	c.mu.Lock()
	c.calls++
	answer, err, delay := c.answer, c.err, c.delay
	c.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	return answer, err
}

func (c *scriptedClient) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newTestFallbackClient(t *testing.T, attempts []FallbackAttempt, opts ...FallbackOption) *FallbackClient {
	t.Helper()
	client, err := NewFallbackClient(attempts, opts...)
	if err != nil {
		t.Fatalf("NewFallbackClient: %v", err)
	}
	return client
}

func chatOnce(t *testing.T, client *FallbackClient) (string, error) {
	t.Helper()
	return client.Chat(context.Background(), []Message{{Role: RoleUser, Content: "hello"}})
}

func TestFallbackPrimarySuccessSkipsFallback(t *testing.T) {
	primary := &scriptedClient{answer: "primary-answer"}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	answer, err := chatOnce(t, client)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != "primary-answer" {
		t.Fatalf("answer = %q, want primary-answer", answer)
	}
	if backup.CallCount() != 0 {
		t.Fatalf("fallback calls = %d, want 0", backup.CallCount())
	}
}

func TestFallbackTimeoutUsesFallback(t *testing.T) {
	timeoutErr := &DegradedError{Err: fmt.Errorf("%w: %v", ErrProviderTimeout, errors.New("slow provider"))}
	primary := &scriptedClient{err: timeoutErr}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	answer, err := chatOnce(t, client)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != "backup-answer" {
		t.Fatalf("answer = %q", answer)
	}
	if primary.CallCount() != 1 || backup.CallCount() != 1 {
		t.Fatalf("calls primary=%d backup=%d, want 1/1", primary.CallCount(), backup.CallCount())
	}
}

func TestFallbackUnavailableUsesFallback(t *testing.T) {
	unavailable := NewDomainError(ErrorUnavailable, "openai", 503, true, errors.New("upstream 503"))
	primary := &scriptedClient{err: &DegradedError{Err: unavailable}}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	if _, err := chatOnce(t, client); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if backup.CallCount() != 1 {
		t.Fatalf("fallback calls = %d, want 1", backup.CallCount())
	}
}

func TestFallbackCircuitOpenUsesFallback(t *testing.T) {
	primary := &scriptedClient{err: fmt.Errorf("%w: cooldown 30s", ErrCircuitOpen)}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	if _, err := chatOnce(t, client); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if backup.CallCount() != 1 {
		t.Fatalf("fallback calls = %d, want 1", backup.CallCount())
	}
}

func TestFallbackInvalidProviderResponseUsesFallback(t *testing.T) {
	invalid := NewDomainError(ErrorInvalidResponse, "openai", 0, false, errors.New("empty response content"))
	primary := &scriptedClient{err: &DegradedError{Err: invalid}}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	if _, err := chatOnce(t, client); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if backup.CallCount() != 1 {
		t.Fatalf("fallback calls = %d, want 1", backup.CallCount())
	}
}

func TestFallbackEmptyAnswerUsesFallback(t *testing.T) {
	primary := &scriptedClient{answer: ""}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	answer, err := chatOnce(t, client)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != "backup-answer" {
		t.Fatalf("answer = %q", answer)
	}
	if backup.CallCount() != 1 {
		t.Fatalf("fallback calls = %d, want 1", backup.CallCount())
	}
}

func TestFallbackEmptyAnswerWithoutFallbackReturnsError(t *testing.T) {
	client := newTestFallbackClient(t, []FallbackAttempt{{Model: "primary-model", Client: &scriptedClient{}}})
	_, err := chatOnce(t, client)
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("err = %v, want ErrEmptyResponse", err)
	}
}

func TestFallbackPermanentErrorSkipsFallback(t *testing.T) {
	authErr := NewDomainError(ErrorAuth, "openai", 401, false, errors.New("invalid api key"))
	primary := &scriptedClient{err: authErr}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	_, err := chatOnce(t, client)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, authErr) {
		t.Fatalf("err = %v, want the original auth error", err)
	}
	if backup.CallCount() != 0 {
		t.Fatalf("fallback calls = %d, want 0 on a permanent failure", backup.CallCount())
	}
}

func TestFallbackGuardrailSkipsFallback(t *testing.T) {
	guardErr := &DegradedError{Err: &GuardError{Kind: GuardKindInput, reason: ErrInjectionDetected}}
	primary := &scriptedClient{err: guardErr}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})

	if _, err := chatOnce(t, client); err == nil {
		t.Fatal("expected a guard error")
	}
	if backup.CallCount() != 0 {
		t.Fatalf("fallback calls = %d, want 0 for a guardrail rejection", backup.CallCount())
	}
}

func TestFallbackCanceledCallerSkipsFallback(t *testing.T) {
	primary := &scriptedClient{err: context.Canceled}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.Chat(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if backup.CallCount() != 0 {
		t.Fatalf("fallback calls = %d, want 0 after caller cancellation", backup.CallCount())
	}
}

func TestFallbackSchemaInvalidUsesFallback(t *testing.T) {
	valid := `{"entities":[{"id":"e1","type":"role","title":"role","evidence":[{"id":"ev1","source_id":"s1","ref_id":"r1","quote":"q"}]}]}`
	invalid := `not-json`
	primary := &scriptedClient{answer: invalid}
	backup := &scriptedClient{answer: valid}
	validate := func(answer string) error {
		if answer == valid {
			return nil
		}
		return errors.New("does not match schema")
	}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	}, WithAnswerValidator(validate))

	answer, err := chatOnce(t, client)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != valid {
		t.Fatalf("answer = %q, want the valid backup answer", answer)
	}
	if backup.CallCount() != 1 {
		t.Fatalf("fallback calls = %d, want 1", backup.CallCount())
	}
}

func TestFallbackAllSchemaInvalidReturnsLastAnswer(t *testing.T) {
	primary := &scriptedClient{answer: "invalid-primary"}
	backup := &scriptedClient{answer: "invalid-backup"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	}, WithAnswerValidator(func(string) error { return errors.New("schema violation") }))

	answer, err := chatOnce(t, client)
	if err != nil {
		t.Fatalf("Chat: %v, want no error so the repair layer can handle it", err)
	}
	if answer != "invalid-backup" {
		t.Fatalf("answer = %q, want the last attempted answer", answer)
	}
	if primary.CallCount() != 1 || backup.CallCount() != 1 {
		t.Fatalf("calls primary=%d backup=%d, want 1/1", primary.CallCount(), backup.CallCount())
	}
}

func TestFallbackChainExhaustedReturnsLastError(t *testing.T) {
	first := &scriptedClient{err: NewDomainError(ErrorUnavailable, "openai", 503, true, nil)}
	second := &scriptedClient{err: NewDomainError(ErrorTimeout, "openai", 0, true, nil)}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: first},
		{Model: "backup-model", Client: second},
	})

	_, err := chatOnce(t, client)
	if Classify(err) != ErrorTimeout {
		t.Fatalf("err = %v, classify = %s, want timeout", err, Classify(err))
	}
	if first.CallCount() != 1 || second.CallCount() != 1 {
		t.Fatalf("calls first=%d second=%d, want 1/1", first.CallCount(), second.CallCount())
	}
}

func TestFallbackNilContext(t *testing.T) {
	client := newTestFallbackClient(t, []FallbackAttempt{{Model: "primary-model", Client: &scriptedClient{answer: "ok"}}})
	if _, err := client.Chat(nil, nil); !errors.Is(err, ErrNilContext) {
		t.Fatalf("err = %v, want ErrNilContext", err)
	}
}

func TestNewFallbackClientValidation(t *testing.T) {
	if _, err := NewFallbackClient(nil); !errors.Is(err, ErrNoFallbackAttempts) {
		t.Fatalf("empty chain err = %v, want ErrNoFallbackAttempts", err)
	}
	if _, err := NewFallbackClient([]FallbackAttempt{{Model: "primary-model"}}); err == nil {
		t.Fatal("expected an error for a nil attempt client")
	}
}

func TestDefaultFallbackPolicy(t *testing.T) {
	live := context.Background()
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "nil error", ctx: live, err: nil, want: false},
		{name: "auth", ctx: live, err: NewDomainError(ErrorAuth, "openai", 401, false, nil), want: false},
		{name: "invalid request 400", ctx: live, err: NewDomainError(ErrorInvalidResponse, "openai", 400, false, nil), want: false},
		{name: "guardrail", ctx: live, err: &GuardError{Kind: GuardKindInput, reason: ErrInjectionDetected}, want: false},
		{name: "plain error", ctx: live, err: errors.New("boom"), want: false},
		{name: "canceled", ctx: live, err: context.Canceled, want: false},
		{name: "canceled context", ctx: canceledCtx, err: NewDomainError(ErrorUnavailable, "openai", 503, true, nil), want: false},
		{name: "retryable status", ctx: live, err: NewDomainError(ErrorRateLimit, "openai", 429, true, nil), want: true},
		{name: "timeout", ctx: live, err: NewDomainError(ErrorTimeout, "openai", 0, true, nil), want: true},
		{name: "unavailable", ctx: live, err: NewDomainError(ErrorUnavailable, "openai", 503, true, nil), want: true},
		{name: "provider timeout sentinel", ctx: live, err: fmt.Errorf("%w: slow", ErrProviderTimeout), want: true},
		{name: "circuit open", ctx: live, err: fmt.Errorf("%w: cooldown", ErrCircuitOpen), want: true},
		{name: "deadline exceeded", ctx: live, err: context.DeadlineExceeded, want: true},
		{name: "invalid response", ctx: live, err: NewDomainError(ErrorInvalidResponse, "openai", 0, false, nil), want: true},
		{name: "empty response", ctx: live, err: ErrEmptyResponse, want: true},
		{name: "degraded timeout", ctx: live, err: &DegradedError{Err: fmt.Errorf("%w: slow", ErrProviderTimeout)}, want: true},
		{name: "degraded auth", ctx: live, err: &DegradedError{Err: NewDomainError(ErrorAuth, "openai", 401, false, nil)}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultFallbackPolicy(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("DefaultFallbackPolicy = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFallbackErrorsDoNotLeakSecrets(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	secretCause := errors.New("authorization: Bearer sk-super-secret rejected")
	providerErr := NewDomainError(ErrorUnavailable, "openai", 503, true, secretCause)
	primary := &scriptedClient{err: &DegradedError{Err: providerErr}}
	backup := &scriptedClient{err: &DegradedError{Err: providerErr}}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	}, WithFallbackLogger(logger))

	messages := []Message{{Role: RoleUser, Content: "SECRET-PROMPT transcript fragment"}}
	_, err := client.Chat(context.Background(), messages)
	if err == nil {
		t.Fatal("expected the chain to fail")
	}
	for _, secret := range []string{"sk-super-secret", "SECRET-PROMPT"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs leaked %q: %s", secret, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "fallback chain exhausted") {
		t.Fatalf("logs do not report the exhausted chain: %s", logs.String())
	}
}

func TestFallbackLogsModelWithoutSecrets(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	providerErr := NewDomainError(ErrorUnavailable, "openai", 503, true, errors.New("api key sk-super-secret rejected"))
	primary := &scriptedClient{err: &DegradedError{Err: providerErr}}
	backup := &scriptedClient{answer: "backup-answer"}
	client := newTestFallbackClient(t, []FallbackAttempt{
		{Model: "primary-model", Client: primary},
		{Model: "backup-model", Client: backup},
	}, WithFallbackLogger(logger))

	messages := []Message{
		{Role: RoleSystem, Content: "system prompt"},
		{Role: RoleUser, Content: "SECRET-PROMPT transcript fragment"},
	}
	answer, err := client.Chat(context.Background(), messages)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != "backup-answer" {
		t.Fatalf("answer = %q", answer)
	}
	logged := logs.String()
	if !strings.Contains(logged, "backup-model") {
		t.Fatalf("logs do not name the fallback model: %s", logged)
	}
	for _, secret := range []string{"sk-super-secret", "SECRET-PROMPT", "system prompt"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("logs leaked %q: %s", secret, logged)
		}
	}
}
