// Package synthesizer merges interview, vacancy and standard role models into a
// single evidence-backed tree and resolves source conflicts deterministically.
//
// The deterministic merge is the source of truth: node ids, hierarchy, evidence,
// conflict winners and source priorities never depend on model output. When a
// chat client is configured the agent additionally asks a model to normalize
// only the human-readable title/description fields of the merged nodes. The
// model answer is applied to existing node ids only, every protected field is
// re-validated against the deterministic result, and any model, parse or
// validation failure keeps the deterministic tree, so a run never fails solely
// because normalization failed.
package synthesizer

import (
	"context"
	"errors"
	"fmt"
	"log"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/prompts"
)

// PromptVersion is the prompt version documented for the synthesizer. The
// deterministic merge does not need it, but the version pins the prompt used by
// the optional normalization pass and keeps the produced tree traceable.
const PromptVersion = prompts.DefaultVersion

// Agent is the synthesizer. It always runs the deterministic merge and
// optionally normalizes the human-readable node fields with an LLM.
type Agent struct {
	client  llm.ChatClient
	version string
	prompt  prompts.Document
}

// Option customises the synthesizer Agent.
type Option func(*Agent)

// WithPromptVersion pins the documented prompt version.
func WithPromptVersion(version string) Option {
	return func(a *Agent) {
		if version != "" {
			a.version = version
		}
	}
}

// WithClient enables the best-effort LLM normalization pass. The client is
// optional: when it is nil the agent stays fully deterministic, which keeps
// tests, batch steps and the pipeline working without a provider.
func WithClient(client llm.ChatClient) Option {
	return func(a *Agent) {
		a.client = client
	}
}

// New creates a synthesizer agent. It validates that the pinned prompt version
// exists so an unknown version cannot silently pass through. A missing client is
// not an error: the agent then never calls a model.
func New(opts ...Option) (*Agent, error) {
	a := &Agent{version: PromptVersion}
	for _, opt := range opts {
		opt(a)
	}
	doc, err := prompts.Load(prompts.KindSynthesizer, a.version)
	if err != nil {
		return nil, fmt.Errorf("synthesizer: %w", err)
	}
	a.prompt = doc
	return a, nil
}

// Version returns the pinned prompt version.
func (a *Agent) Version() string { return a.version }

// Synthesize merges the three sources into one tree, resolves conflicts, emits
// conflict messages and validates the result against the output contract.
//
// When a client is configured it then asks the model to normalize titles and
// descriptions of the merged nodes. Normalization is best-effort: an LLM,
// parsing or validation failure is logged and the deterministic tree is
// returned unchanged. The model can never add or remove nodes, change parent
// links, evidence, ids or conflict winners.
func (a *Agent) Synthesize(ctx context.Context, in agent.SynthesizerInput) (agent.SynthesizerOutput, error) {
	if ctx == nil {
		return agent.SynthesizerOutput{}, errors.New("synthesizer: nil context")
	}
	if err := ctx.Err(); err != nil {
		return agent.SynthesizerOutput{}, fmt.Errorf("synthesizer: %w", err)
	}
	deterministic, err := Merge(in)
	if err != nil {
		return agent.SynthesizerOutput{}, err
	}
	if err := validateSynthesizedOutput(deterministic); err != nil {
		return agent.SynthesizerOutput{}, fmt.Errorf("synthesizer: output violates contract: %w", err)
	}
	if a.client == nil {
		return deterministic, nil
	}
	normalized, err := a.normalize(ctx, in.Profession, deterministic)
	if err != nil {
		// The merge already produced a valid, evidence-backed tree. Losing it
		// because a cosmetic normalization call failed would be worse than
		// shipping the deterministic result.
		log.Printf("synthesizer: LLM normalization skipped: %v", err)
		return deterministic, nil
	}
	return normalized, nil
}
