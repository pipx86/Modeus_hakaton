package agents

import (
	"fmt"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/llm"
	agentmetrics "activity-descriptor/internal/llm/metrics"
	"activity-descriptor/internal/llm/openai"
)

// ClientDecorator wraps every per-model client while a pool is built. model is
// the exact provider model that client calls, so an observability decorator
// (for example llmobs.Recorder) records the truthful model for each fallback
// attempt instead of pretending the fallback answer came from the primary.
type ClientDecorator func(agent AgentName, model string, client llm.ChatClient) llm.ChatClient

// PoolOption customises NewPool and NewPoolWithGate.
type PoolOption func(*poolOptions)

type poolOptions struct {
	decorate ClientDecorator
}

// WithClientDecorator attaches an observability decorator to every per-model
// client, including fallback models.
func WithClientDecorator(decorate ClientDecorator) PoolOption {
	return func(options *poolOptions) {
		if decorate != nil {
			options.decorate = decorate
		}
	}
}

// Pool holds one gated client per product agent.
type Pool struct {
	gate    *Gate
	clients map[AgentName]llm.ChatClient
	metrics *agentmetrics.Registry
}

// NewPool builds a gated client for every agent. All clients share one
// concurrency limit and one request-rate limiter.
func NewPool(cfg Config, opts ...PoolOption) (*Pool, error) {
	gate, err := NewGate(cfg.MaxConcurrency, cfg.RequestsPerMinute, cfg.RetryMax)
	if err != nil {
		return nil, err
	}
	return NewPoolWithGate(cfg, gate, opts...)
}

// NewPoolWithGate builds the clients around an existing gate. It is useful for
// tests that need to inspect or share the gate.
func NewPoolWithGate(cfg Config, gate *Gate, opts ...PoolOption) (*Pool, error) {
	if gate == nil {
		return nil, fmt.Errorf("agents: nil gate")
	}
	var options poolOptions
	for _, opt := range opts {
		opt(&options)
	}
	registry := agentmetrics.New()
	pool := &Pool{gate: gate, clients: make(map[AgentName]llm.ChatClient, len(Names())), metrics: registry}
	for _, name := range Names() {
		spec, ok := cfg.Agents[name]
		if !ok {
			return nil, fmt.Errorf("agents: missing spec for %s", name)
		}
		primary, err := buildAgentClient(cfg, gate, name, spec, registry, options.decorate)
		if err != nil {
			return nil, fmt.Errorf("agents: client for %s: %w", name, err)
		}
		client := primary
		// A duplicate fallback would call the same model twice. Skip the chain
		// instead of failing the whole pool: config parsing already normalizes
		// it, and a direct Config must not turn a harmless duplicate into an
		// LLM outage.
		if spec.FallbackModel != "" && spec.FallbackModel != spec.Model {
			client, err = buildFallbackChain(cfg, gate, name, spec, primary, registry, options.decorate)
			if err != nil {
				return nil, err
			}
		}
		pool.clients[name] = client
	}
	return pool, nil
}

// buildFallbackChain builds the optional second model with the same provider,
// guardrails, circuit breaker and shared gate, then wraps both attempts in a
// fallback chain. Schema validation is wired so a model that violates the agent
// output contract does not win over the other model, while transport errors keep
// their usual classification.
func buildFallbackChain(cfg Config, gate *Gate, name AgentName, primarySpec Spec, primary llm.ChatClient, registry *agentmetrics.Registry, decorate ClientDecorator) (llm.ChatClient, error) {
	fallbackSpec := primarySpec
	fallbackSpec.Model = primarySpec.FallbackModel
	fallback, err := buildAgentClient(cfg, gate, name, fallbackSpec, registry, decorate)
	if err != nil {
		return nil, fmt.Errorf("agents: fallback client for %s: %w", name, err)
	}
	chain, err := llm.NewFallbackClient(
		[]llm.FallbackAttempt{
			{Model: primarySpec.Model, Client: primary},
			{Model: primarySpec.FallbackModel, Client: fallback},
		},
		llm.WithAnswerValidator(answerValidator(name)),
	)
	if err != nil {
		return nil, fmt.Errorf("agents: fallback chain for %s: %w", name, err)
	}
	return chain, nil
}

// buildAgentClient builds one complete per-model call pipeline: provider client,
// guardrails, circuit breaker with the per-agent timeout, the gate around every
// attempt, tracing and an optional observability decorator.
func buildAgentClient(cfg Config, gate *Gate, name AgentName, spec Spec, registry *agentmetrics.Registry, decorate ClientDecorator) (llm.ChatClient, error) {
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
		return nil, err
	}
	guarded, err := llm.NewGuardedClient(raw, llm.WithOutputLeakRetry(1))
	if err != nil {
		return nil, fmt.Errorf("guardrails: %w", err)
	}
	breaker, err := llm.NewCircuitBreaker(guarded, llm.WithTimeout(spec.Timeout))
	if err != nil {
		return nil, fmt.Errorf("circuit breaker: %w", err)
	}
	runner, err := NewRunnerWithMetrics(name, breaker, gate, spec.Model, spec.ReasoningEffort, registry)
	if err != nil {
		return nil, err
	}
	var client llm.ChatClient = NewTracedClient(runner, name, spec.Model, spec.ReasoningEffort)
	if decorate != nil {
		client = decorate(name, spec.Model, client)
	}
	return client, nil
}

// answerValidator returns the JSON-schema validator of the agent output
// contract. It is used only to choose between two models; the repair layer
// above the chain remains authoritative and reports terminal validation errors.
func answerValidator(name AgentName) llm.AnswerValidator {
	var kind agent.Kind
	switch name {
	case Interviewer:
		kind = agent.KindInterviewer
	case Analyst:
		kind = agent.KindAnalyst
	case Synthesizer:
		kind = agent.KindSynthesizer
	default:
		return nil
	}
	return func(answer string) error {
		return agent.ValidateOutput(kind, []byte(answer))
	}
}

// Client returns the gated client for an agent. When a fallback model is
// configured, the returned client retries transient primary failures on it.
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
