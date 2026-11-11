package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
)

// scriptedBreakerClient returns queued errors in order, then success.
type scriptedBreakerClient struct {
	errs  []error
	calls int
	block bool
}

func (c *scriptedBreakerClient) Chat(ctx context.Context, _ []Message) (string, error) {
	c.calls++
	if c.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	index := c.calls - 1
	if index < len(c.errs) && c.errs[index] != nil {
		return "", c.errs[index]
	}
	return "ok", nil
}

func TestCircuitBreakerOpensOnProviderErrors(t *testing.T) {
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	client := &scriptedBreakerClient{errs: []error{
		errors.New("provider returned status 500"),
		errors.New("provider returned status 502"),
	}}
	breaker, err := NewCircuitBreaker(client,
		WithFailureThreshold(2),
		WithCooldown(time.Hour),
		WithTimeout(5*time.Second),
		WithClock(now),
	)
	if err != nil {
		t.Fatalf("NewCircuitBreaker: %v", err)
	}
	start := time.Now()
	if _, err := breaker.Chat(context.Background(), nil); err == nil {
		t.Fatal("first failure returned nil error")
	}
	if got := breaker.State(); got != BreakerClosed {
		t.Fatalf("state after one failure = %q, want closed", got)
	}
	if _, err := breaker.Chat(context.Background(), nil); err == nil {
		t.Fatal("second failure returned nil error")
	}
	if got := breaker.State(); got != BreakerOpen {
		t.Fatalf("state after threshold = %q, want open", got)
	}
	_, err = breaker.Chat(context.Background(), nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("open breaker error = %v, want ErrCircuitOpen", err)
	}
	if client.calls != 2 {
		t.Fatalf("wrapped client calls = %d, want 2 (open breaker must not call it)", client.calls)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("open breaker took %s, want immediate error", elapsed)
	}

	// After the cooldown the breaker probes; a successful probe closes it.
	clock = clock.Add(2 * time.Hour)
	answer, err := breaker.Chat(context.Background(), nil)
	if err != nil || answer != "ok" {
		t.Fatalf("probe answer = %q, %v; want ok", answer, err)
	}
	if got := breaker.State(); got != BreakerClosed {
		t.Fatalf("state after successful probe = %q, want closed", got)
	}
	if client.calls != 3 {
		t.Fatalf("wrapped client calls = %d, want 3", client.calls)
	}
}

func TestCircuitBreakerHalfOpenFailureReopens(t *testing.T) {
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	client := &scriptedBreakerClient{errs: []error{
		errors.New("status 500"),
		errors.New("status 500"),
	}}
	breaker, err := NewCircuitBreaker(client,
		WithFailureThreshold(1),
		WithCooldown(time.Minute),
		WithClock(now),
	)
	if err != nil {
		t.Fatalf("NewCircuitBreaker: %v", err)
	}
	if _, err := breaker.Chat(context.Background(), nil); err == nil {
		t.Fatal("first failure returned nil error")
	}
	if got := breaker.State(); got != BreakerOpen {
		t.Fatalf("state = %q, want open", got)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := breaker.Chat(context.Background(), nil); err == nil {
		t.Fatal("failed probe returned nil error")
	}
	if got := breaker.State(); got != BreakerOpen {
		t.Fatalf("state after failed probe = %q, want open", got)
	}
	if _, err := breaker.Chat(context.Background(), nil); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("error = %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreakerTimeoutDoesNotHang(t *testing.T) {
	client := &scriptedBreakerClient{block: true}
	breaker, err := NewCircuitBreaker(client,
		WithFailureThreshold(1),
		WithTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewCircuitBreaker: %v", err)
	}
	start := time.Now()
	_, err = breaker.Chat(context.Background(), nil)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrProviderTimeout) {
		t.Fatalf("timeout error = %v, want ErrProviderTimeout", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call took %s, run must not hang", elapsed)
	}
	if got := breaker.State(); got != BreakerOpen {
		t.Fatalf("state after timeout = %q, want open", got)
	}
}

func TestCircuitBreakerNilContext(t *testing.T) {
	breaker, err := NewCircuitBreaker(&scriptedBreakerClient{})
	if err != nil {
		t.Fatalf("NewCircuitBreaker: %v", err)
	}
	var target *DegradedError
	err = func() error {
		_, err := breaker.Chat(nil, nil)
		return err
	}()
	if !errors.As(err, &target) {
		t.Fatalf("nil context error = %v, want DegradedError", err)
	}
}

func TestNewCircuitBreakerRequiresClient(t *testing.T) {
	if _, err := NewCircuitBreaker(nil); err == nil {
		t.Fatal("NewCircuitBreaker(nil) accepted, want error")
	}
}

func TestMarkRunDegraded(t *testing.T) {
	run := &domain.Run{ID: "run-1", Status: domain.RunRunning}
	plain := errors.New("plain error")
	if MarkRunDegraded(run, plain) {
		t.Fatal("plain error marked the run degraded")
	}
	if run.Status != domain.RunRunning {
		t.Fatalf("status = %q, want running", run.Status)
	}
	if MarkRunDegraded(nil, &DegradedError{Err: plain}) {
		t.Fatal("nil run was marked degraded")
	}
	degraded := &DegradedError{Err: ErrCircuitOpen}
	if !MarkRunDegraded(run, degraded) {
		t.Fatal("degraded error was not detected")
	}
	if run.Status != domain.RunDegraded {
		t.Fatalf("status = %q, want degraded", run.Status)
	}
	if !degraded.Degraded() {
		t.Fatal("Degraded() = false, want true")
	}
	if !errors.Is(degraded, ErrCircuitOpen) {
		t.Fatal("DegradedError does not unwrap to ErrCircuitOpen")
	}
}
