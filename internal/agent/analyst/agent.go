// Package analyst extracts evidence-backed role-model entities from interview
// transcripts and links them to the curated taxonomy.
package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/repair"
	"activity-descriptor/internal/prompts"
)

// PromptVersion is the default prompt version used by the analyst agent.
const PromptVersion = prompts.DefaultVersion

// ErrModel marks a model, prompt or validation failure that survived repair.
var ErrModel = errors.New("analyst: model failure")

// Agent extracts entities from one transcript and links them to the taxonomy.
type Agent struct {
	client     llm.ChatClient
	version    string
	maxRepairs int
	taxonomy   Taxonomy
}

// Option customises the analyst Agent.
type Option func(*Agent)

// WithTaxonomy replaces the embedded taxonomy fixture.
func WithTaxonomy(taxonomy Taxonomy) Option {
	return func(a *Agent) { a.taxonomy = taxonomy }
}

// WithPromptVersion pins the prompt version loaded from the embedded prompts.
func WithPromptVersion(version string) Option {
	return func(a *Agent) {
		if version != "" {
			a.version = version
		}
	}
}

// WithMaxRepairs sets the number of repair attempts for invalid model output.
// Negative values are ignored.
func WithMaxRepairs(maxRepairs int) Option {
	return func(a *Agent) {
		if maxRepairs >= 0 {
			a.maxRepairs = maxRepairs
		}
	}
}

// New creates an analyst agent. client is required; the embedded taxonomy is
// used unless WithTaxonomy is passed.
func New(client llm.ChatClient, opts ...Option) (*Agent, error) {
	if client == nil {
		return nil, errors.New("analyst: chat client is required")
	}
	a := &Agent{client: client, version: PromptVersion, maxRepairs: 1}
	for _, opt := range opts {
		opt(a)
	}
	if a.taxonomy.Version == "" {
		taxonomy, err := DefaultTaxonomy()
		if err != nil {
			return nil, fmt.Errorf("analyst: %w", err)
		}
		a.taxonomy = taxonomy
	}
	if _, err := prompts.Load(prompts.KindAnalyst, a.version); err != nil {
		return nil, fmt.Errorf("analyst: %w", err)
	}
	return a, nil
}

// Version returns the pinned prompt version.
func (a *Agent) Version() string { return a.version }

// Taxonomy returns the taxonomy used for linking.
func (a *Agent) Taxonomy() Taxonomy { return a.taxonomy }

// Extract asks the model for entities. The validator enforces the output schema
// and that every quote is a verbatim transcript fragment; invalid answers are
// repaired before a terminal ErrModel is returned.
func (a *Agent) Extract(ctx context.Context, in agent.AnalystInput) (agent.AnalystOutput, error) {
	doc, err := prompts.Load(prompts.KindAnalyst, a.version)
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}
	messages, err := buildMessages(doc, in)
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}

	validator := func(answer string) error {
		if err := agent.ValidateOutput(agent.KindAnalyst, []byte(answer)); err != nil {
			return err
		}
		var out agent.AnalystOutput
		if err := json.Unmarshal([]byte(answer), &out); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		return ValidateEntities(out, in.Transcript)
	}
	repaired, err := repair.New(a.client, validator, repair.WithMaxRepairs(a.maxRepairs))
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}
	answer, err := repaired.Chat(ctx, messages)
	if err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: %v", ErrModel, err)
	}

	var out agent.AnalystOutput
	if err := json.Unmarshal([]byte(answer), &out); err != nil {
		return agent.AnalystOutput{}, fmt.Errorf("%w: decode: %v", ErrModel, err)
	}
	return out, nil
}

// Analyze extracts entities, normalizes them, removes duplicates and links them
// to the taxonomy. The result is a connected role → function → task → action
// tree in which every node has evidence.
func (a *Agent) Analyze(ctx context.Context, in agent.AnalystInput) (agent.AnalystOutput, error) {
	extracted, err := a.Extract(ctx, in)
	if err != nil {
		return agent.AnalystOutput{}, err
	}
	entities := DedupEntities(NormalizeEntities(extracted.Entities))
	nodes := a.taxonomy.Link(ToNodes(entities))
	if len(nodes) == 0 {
		return agent.AnalystOutput{}, ErrNoEntities
	}
	return agent.AnalystOutput{Entities: NodesToEntities(nodes)}, nil
}

func buildMessages(doc prompts.Document, in agent.AnalystInput) ([]llm.Message, error) {
	payload, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}
	system := doc.System + "\n\n---\n\n# Примеры\n\n" + doc.Examples
	user := "Вход аналитика (JSON):\n" + string(payload) +
		"\n\nВерни JSON по схеме analyst.output.schema.json. " +
		"Каждая цитата должна дословно присутствовать в transcript."
	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: user},
	}, nil
}
