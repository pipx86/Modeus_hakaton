package agents

import (
	"fmt"

	"activity-descriptor/internal/llm"
	agentmetrics "activity-descriptor/internal/llm/metrics"
	"activity-descriptor/internal/llm/openai"
)

// Pool holds one gated client per product agent.
type Pool struct {
	gate    *Gate
	clients map[AgentName]llm.ChatClient
	metrics *agentmetrics.Registry
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
	registry := agentmetrics.New()
	pool := &Pool{gate: gate, clients: make(map[AgentName]llm.ChatClient, len(Names())), metrics: registry}
	for _, name := range Names() {
		spec, ok := cfg.Agents[name]
		if !ok {
			return nil, fmt.Errorf("agents: missing spec for %s", name)
		}
		temperature := spec.Temperature
		agentName := string(name)
		raw, err := openai.New(openai.Config{
			BaseURL:         cfg.BaseURL,
			APIKey:          cfg.APIKey,
			Model:           spec.Model,
			Timeout:         spec.Timeout,
			MaxTokens:       spec.MaxTokens,
			Temperature:     &temperature,
			ReasoningEffort: spec.ReasoningEffort,
		}, openai.WithUsageObserver(func(u llm.Usage) {
			registry.ObserveUsage(agentName, u)
		}))
		if err != nil {
			return nil, fmt.Errorf("agents: client for %s: %w", name, err)
		}
		guarded, err := llm.NewGuardedClient(raw, llm.WithOutputLeakRetry(1))
		if err != nil {
			return nil, fmt.Errorf("agents: guardrails for %s: %w", name, err)
		}
		breaker, err := llm.NewCircuitBreaker(guarded, llm.WithTimeout(spec.Timeout))
		if err != nil {
			return nil, fmt.Errorf("agents: circuit breaker for %s: %w", name, err)
		}
		runner, err := NewRunnerWithMetrics(name, breaker, gate, spec.Model, spec.ReasoningEffort, registry)
		if err != nil {
			return nil, err
		}
		pool.clients[name] = NewTracedClient(runner, name, spec.Model, spec.ReasoningEffort)
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

// Metrics returns the shared agent metrics registry.
func (p *Pool) Metrics() *agentmetrics.Registry {
	if p == nil {
		return nil
	}
	return p.metrics
}

// Gate returns the shared gate.
func (p *Pool) Gate() *Gate {
	if p == nil {
		return nil
	}
	return p.gate
}
