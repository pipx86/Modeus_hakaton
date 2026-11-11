package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"activity-descriptor/internal/domain"
)

// BreakerState is the state of a circuit breaker.
type BreakerState string

// Circuit breaker states.
const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// String returns the state as a plain string.
func (s BreakerState) String() string { return string(s) }

// ErrCircuitOpen is returned without calling the provider while the breaker is
// open and the cooldown has not elapsed.
var ErrCircuitOpen = errors.New("llm: circuit open")

// ErrProviderTimeout marks a provider call that exceeded the breaker timeout.
var ErrProviderTimeout = errors.New("llm: provider timeout")

// ErrNilContext marks a Chat call without a context.
var ErrNilContext = errors.New("llm: nil context")

// DegradedError marks a call that failed because the model was unavailable.
// The agent layer converts it into a fallback and marks the run degraded.
type DegradedError struct {
	Err error
}

// Error implements error.
func (e *DegradedError) Error() string {
	if e == nil || e.Err == nil {
		return "llm: degraded"
	}
	return "llm: degraded: " + e.Err.Error()
}

// Unwrap allows errors.Is/errors.As to reach the provider error.
func (e *DegradedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Degraded reports that the run continued in degraded mode.
func (e *DegradedError) Degraded() bool { return e != nil }

// MarkRunDegraded sets the run status to degraded when err is a DegradedError.
// It returns whether the status was changed.
func MarkRunDegraded(run *domain.Run, err error) bool {
	var degraded *DegradedError
	if run == nil || !errors.As(err, &degraded) {
		return false
	}
	run.Status = domain.RunDegraded
	return true
}

// Default circuit breaker settings.
const (
	// DefaultFailureThreshold is the number of consecutive failures that opens
	// the breaker.
	DefaultFailureThreshold = 3
	// DefaultCooldown is how long the breaker stays open before a probe.
	DefaultCooldown = 30 * time.Second
	// DefaultTimeout caps one provider call so a run can never hang forever.
	DefaultTimeout = 60 * time.Second
)

// CircuitBreaker wraps a ChatClient and stops calling it after repeated
// failures. It also enforces a per-call timeout so a run cannot hang.
type CircuitBreaker struct {
	next             ChatClient
	failureThreshold int
	cooldown         time.Duration
	timeout          time.Duration
	now              func() time.Time

	mu       sync.Mutex
	state    BreakerState
	failures int
	openedAt time.Time
	probing  bool
}

// BreakerOption customises CircuitBreaker.
type BreakerOption func(*CircuitBreaker)

// WithFailureThreshold sets how many consecutive failures open the breaker.
func WithFailureThreshold(threshold int) BreakerOption {
	return func(b *CircuitBreaker) {
		if threshold > 0 {
			b.failureThreshold = threshold
		}
	}
}

// WithCooldown sets how long the breaker stays open.
func WithCooldown(cooldown time.Duration) BreakerOption {
	return func(b *CircuitBreaker) {
		if cooldown > 0 {
			b.cooldown = cooldown
		}
	}
}

// WithTimeout caps one provider call. A non-positive value is ignored.
func WithTimeout(timeout time.Duration) BreakerOption {
	return func(b *CircuitBreaker) {
		if timeout > 0 {
			b.timeout = timeout
		}
	}
}

// WithClock replaces the time source. It exists for deterministic tests.
func WithClock(now func() time.Time) BreakerOption {
	return func(b *CircuitBreaker) {
		if now != nil {
			b.now = now
		}
	}
}

// NewCircuitBreaker creates a breaker around next. next is required.
func NewCircuitBreaker(next ChatClient, opts ...BreakerOption) (*CircuitBreaker, error) {
	if next == nil {
		return nil, errors.New("llm: circuit breaker requires a client")
	}
	breaker := &CircuitBreaker{
		next:             next,
		failureThreshold: DefaultFailureThreshold,
		cooldown:         DefaultCooldown,
		timeout:          DefaultTimeout,
		now:              time.Now,
		state:            BreakerClosed,
	}
	for _, opt := range opts {
		opt(breaker)
	}
	return breaker, nil
}

// State returns the current breaker state.
func (b *CircuitBreaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Failures returns the number of consecutive failures.
func (b *CircuitBreaker) Failures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// Chat calls the wrapped client unless the breaker is open. Provider errors and
// timeouts are wrapped in DegradedError so callers can mark the run degraded.
func (b *CircuitBreaker) Chat(ctx context.Context, messages []Message) (string, error) {
	if ctx == nil {
		return "", &DegradedError{Err: ErrNilContext}
	}
	if err := b.acquire(); err != nil {
		return "", err
	}

	callCtx := ctx
	if b.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, b.timeout)
		defer cancel()
	}

	answer, err := b.next.Chat(callCtx, messages)
	if err == nil {
		b.recordSuccess()
		return answer, nil
	}
	b.recordFailure()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return "", &DegradedError{Err: fmt.Errorf("%w: %v", ErrProviderTimeout, err)}
	}
	return "", &DegradedError{Err: err}
}

func (b *CircuitBreaker) acquire() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerOpen:
		if b.now().Sub(b.openedAt) < b.cooldown {
			return fmt.Errorf("%w: cooldown %s", ErrCircuitOpen, b.cooldown)
		}
		b.state = BreakerHalfOpen
		b.probing = true
	case BreakerHalfOpen:
		if b.probing {
			return fmt.Errorf("%w: probe already in flight", ErrCircuitOpen)
		}
		b.probing = true
	}
	return nil
}

func (b *CircuitBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = BreakerClosed
	b.probing = false
}

func (b *CircuitBreaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.probing = false
	if b.state == BreakerHalfOpen || b.failures >= b.failureThreshold {
		b.state = BreakerOpen
		b.openedAt = b.now()
	}
}
