package agents

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Limiter is a token bucket that paces requests to a provider. It is safe for
// concurrent use.
type Limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

// NewLimiter creates a limiter for the given requests per minute.
func NewLimiter(requestsPerMinute int) (*Limiter, error) {
	if requestsPerMinute <= 0 {
		return nil, fmt.Errorf("agents: requests per minute must be positive")
	}
	rate := float64(requestsPerMinute) / 60.0
	burst := float64(requestsPerMinute) / 4.0
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		rate:   rate,
		burst:  burst,
		tokens: burst,
		last:   time.Now(),
	}, nil
}

// Wait blocks until a token is available or ctx is done.
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		now := time.Now()
		if l.last.IsZero() {
			l.last = now
		}
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		waitSeconds := (1 - l.tokens) / l.rate
		l.mu.Unlock()
		wait := time.Duration(waitSeconds * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
