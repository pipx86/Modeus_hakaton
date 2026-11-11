package agents

import (
	"fmt"

	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/openai"
)

// Pool holds one gated client per product agent.
type Pool struct {
	gate    *Gate
	clients map[AgentName]llm.ChatClient
}

// NewPool builds a gated client for every agent. All clients share one
// concurrency limit and one request-rate limiter.
func NewPool(cfg Config) (*Pool, error) {
	gate, err := NewGate(cfg.MaxConcurrency, cfg.RequestsPerMinute, cfg.RetryMax)
	if err != nil {
		return nil, err
	}
	return NewPoolWithGate(cfg, gate)
}

// NewPoolWithGate builds the clients around an existing gate. It is useful for
// tests that need to inspect or share the gate.
func NewPoolWithGate(cfg Config, gate *Gate) (*Pool, error) {
	if gate == nil {
		return nil, fmt.Errorf("agents: nil gate")
	}
	pool := &Pool{gate: gate, clients: make(map[AgentName]llm.ChatClient, len(Names()))}
	for _, name := range Names() {
		spec, ok := cfg.Agents[name]
		if !ok {
			return nil, fmt.Errorf("agents: missing spec for %s", name)
		}
		temperature := spec.Temperature
		raw, err := openai.New(openai.Config{
			BaseURL:         cfg.BaseURL,
			APIKey:          cfg.APIKey,
			Model:           spec.Model,
			Timeout:         spec.Timeout,
			MaxTokens:       spec.MaxTokens,
			Temperature:     &temperature,
			ReasoningEffort: spec.ReasoningEffort,
		})
		if err != nil {
			return nil, fmt.Errorf("agents: client for %s: %w", name, err)
		}
		guarded, err := llm.NewGuardedClient(raw)
		if err != nil {
			return nil, fmt.Errorf("agents: guardrails for %s: %w", name, err)
		}
		breaker, err := llm.NewCircuitBreaker(guarded)
		if err != nil {
			return nil, fmt.Errorf("agents: circuit breaker for %s: %w", name, err)
		}
		runner, err := NewRunner(name, breaker, gate)
		if err != nil {
			return nil, err
		}
		pool.clients[name] = runner
	}
	return pool, nil
}

// Client returns the gated client for an agent.
func (p *Pool) Client(name AgentName) (llm.ChatClient, error) {
	if p == nil {
		return nil, fmt.Errorf("agents: nil pool")
	}
	client, ok := p.clients[name]
	if !ok {
		return nil, fmt.Errorf("agents: unknown agent %q", name)
	}
	return client, nil
}

// Clients returns all gated clients keyed by agent name.
func (p *Pool) Clients() map[AgentName]llm.ChatClient {
	if p == nil {
		return nil
	}
	copy := make(map[AgentName]llm.ChatClient, len(p.clients))
	for name, client := range p.clients {
		copy[name] = client
	}
	return copy
}

// Gate returns the shared gate.
func (p *Pool) Gate() *Gate {
	if p == nil {
		return nil
	}
	return p.gate
}
