// Package synthesizer merges interview, vacancy and standard role models into a
// single evidence-backed tree and resolves source conflicts deterministically.
//
// Merging is rule-based on purpose: the evidence guarantees and the conflict
// priority order (interview > standard > vacancy) must not depend on model
// output. A model-backed normalization pass can be added later without changing
// the output contract.
package synthesizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/prompts"
)

// PromptVersion is the prompt version documented for the synthesizer. The
// deterministic merge does not call a model, but the version is kept for
// traceability of future normalization passes.
const PromptVersion = prompts.DefaultVersion

// Agent is the deterministic synthesizer.
type Agent struct {
	version string
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

// New creates a synthesizer agent. It validates that the pinned prompt version
// exists so an unknown version cannot silently pass through.
func New(opts ...Option) (*Agent, error) {
	a := &Agent{version: PromptVersion}
	for _, opt := range opts {
		opt(a)
	}
	if _, err := prompts.Load(prompts.KindSynthesizer, a.version); err != nil {
		return nil, fmt.Errorf("synthesizer: %w", err)
	}
	return a, nil
}

// Version returns the pinned prompt version.
func (a *Agent) Version() string { return a.version }

// Synthesize merges the three sources into one tree, resolves conflicts, emits
// conflict messages and validates the result against the output contract.
func (a *Agent) Synthesize(ctx context.Context, in agent.SynthesizerInput) (agent.SynthesizerOutput, error) {
	if ctx == nil {
		return agent.SynthesizerOutput{}, errors.New("synthesizer: nil context")
	}
	if err := ctx.Err(); err != nil {
		return agent.SynthesizerOutput{}, fmt.Errorf("synthesizer: %w", err)
	}
	out, err := Merge(in)
	if err != nil {
		return agent.SynthesizerOutput{}, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return agent.SynthesizerOutput{}, fmt.Errorf("synthesizer: marshal output: %w", err)
	}
	if err := agent.ValidateOutput(agent.KindSynthesizer, data); err != nil {
		return agent.SynthesizerOutput{}, fmt.Errorf("synthesizer: output violates contract: %w", err)
	}
	return out, nil
}
