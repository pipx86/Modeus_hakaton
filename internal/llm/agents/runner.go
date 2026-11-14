package agents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"activity-descriptor/internal/llm"
)

// Gate is the shared parallel/concurrency/rate-limit gate for all agent calls.
type Gate struct {
	sem      chan struct{}
	limiter  *Limiter
	retryMax int
	backoff  time.Duration
}

// NewGate builds a gate with at most maxConcurrency in-flight calls and a
// provider-wide request rate limit.
func NewGate(maxConcurrency, requestsPerMinute, retryMax int) (*Gate, error) {
	if maxConcurrency <= 0 {
		return nil, fmt.Errorf("agents: max concurrency must be positive")
	}
	limiter, err := NewLimiter(requestsPerMinute)
	if err != nil {
		return nil, err
	}
	return &Gate{
		sem:      make(chan struct{}, maxConcurrency),
		limiter:  limiter,
		retryMax: retryMax,
		backoff:  2 * time.Second,
	}, nil
}

// Runner wraps one agent's ChatClient with the shared gate.
type Runner struct {
	name AgentName
	next llm.ChatClient
	gate *Gate
}

// NewRunner creates a gated client for one agent.
func NewRunner(name AgentName, next llm.ChatClient, gate *Gate) (*Runner, error) {
	if next == nil {
		return nil, fmt.Errorf("agents: nil client for %s", name)
	}
	if gate == nil {
		return nil, fmt.Errorf("agents: nil gate")
	}
	return &Runner{name: name, next: next, gate: gate}, nil
}

// Name returns the agent name.
func (r *Runner) Name() AgentName { return r.name }

// Chat calls the provider through the shared concurrency and rate limit gate.
// Retryable provider errors (429 and 5xx) are retried with exponential backoff.
func (r *Runner) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("agents: nil context")
	}

	select {
	case r.gate.sem <- struct{}{}:
		defer func() { <-r.gate.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := r.gate.limiter.Wait(ctx); err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt <= r.gate.retryMax; attempt++ {
		if attempt > 0 {
			delay := r.gate.backoff * time.Duration(1<<(attempt-1))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
		answer, err := r.next.Chat(ctx, messages)
		if err == nil {
			return answer, nil
		}
		lastErr = err
		if ctx.Err() != nil || !retryable(err) {
			return "", err
		}
	}
	return "", lastErr
}

func retryable(err error) bool {
	return llm.IsRetryable(err)
}

// Job is one independent model call executed by Parallel.
type Job struct {
	Name     string
	Messages []llm.Message
}

// Result is the outcome of one parallel job.
type Result struct {
	Name     string
	Answer   string
	Err      error
	Duration time.Duration
}

// Parallel runs independent jobs through the shared gate and returns results in
// the same order as jobs. Concurrency is bounded by the gate's semaphore.
func (g *Gate) Parallel(ctx context.Context, client llm.ChatClient, jobs []Job) []Result {
	results := make([]Result, len(jobs))
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			start := time.Now()
			answer, err := client.Chat(ctx, jobs[index].Messages)
			results[index] = Result{
				Name:     jobs[index].Name,
				Answer:   answer,
				Err:      err,
				Duration: time.Since(start),
			}
		}(i)
	}
	wg.Wait()
	return results
}
