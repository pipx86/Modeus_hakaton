package steps

import (
	"time"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

const (
	// DefaultBatchMaxRetries is the number of retries of one batch step. The
	// orchestrator therefore executes each step at most DefaultBatchMaxRetries+1
	// times, matching the pre-existing handler/resume behaviour.
	DefaultBatchMaxRetries = 2
	// DefaultAIStepTimeout is the per-attempt budget of one LLM-backed batch
	// step. The analyst and synthesizer calls need minutes, not the 30 s that
	// was enough for the deterministic vacancy pipeline.
	DefaultAIStepTimeout = 8 * time.Minute
	// DefaultVacancyStepTimeout keeps the old vacancy-only pipeline budget.
	DefaultVacancyStepTimeout = 30 * time.Second
)

// BatchPlan is the shared configuration of the batch domain.Orchestrate path.
//
// It holds both possible step chains: the AI agent chain (interview no-op →
// analyst → synthesizer → tree persistence) and the legacy vacancy-driven chain.
// The create-run handler and the resume processor both take a plan from
// cmd/server and call For, so the two paths cannot diverge.
type BatchPlan struct {
	// AI is empty when the server has no LLM wiring (or in tests).
	AI []domain.Step
	// Fallback is the vacancy-only chain used without a transcript or an LLM.
	Fallback []domain.Step

	AITimeout       time.Duration
	FallbackTimeout time.Duration
	// MaxRetries is passed to domain.Orchestrate.
	MaxRetries int
}

// HasAI reports whether the agent-backed chain is available.
func (p BatchPlan) HasAI() bool { return len(p.AI) > 0 }

// For returns the ordered steps and the per-attempt timeout for one batch run.
// The AI chain is selected only when it is configured and the run has a
// transcript; otherwise the vacancy fallback keeps the historical behaviour.
func (p BatchPlan) For(hasTranscript bool) ([]domain.Step, time.Duration) {
	if hasTranscript && len(p.AI) > 0 {
		return p.AI, p.AITimeout
	}
	return p.Fallback, p.FallbackTimeout
}

// BatchOptions are the dependencies of the batch orchestrator, supplied by the
// server wiring in cmd/server.
type BatchOptions struct {
	// Analyst is the LLM-backed analyst client from the agent pool. nil disables
	// the AI chain.
	Analyst llm.ChatClient
	// AnalystPromptVersion is the active analyst prompt version ("" means v1).
	AnalystPromptVersion string
	// Synthesizer is the deterministic merge agent (nil disables the AI chain).
	Synthesizer Synthesizer
	// ZunEnricher optionally fills knowledge/skills/abilities after synthesis.
	ZunEnricher ZunEnricher
	// Store persists the AI tree and its interview source, and is also used by
	// the vacancy review step. nil disables both tree writes.
	Store AITreeStore

	// Vacancy fallback dependencies. Zero values reproduce the historical
	// defaults (IT catalog, single default profile, top 10 skills).
	VacancyCatalog  *analysis.Catalog
	VacancyProfiles []analysis.CandidateProfile
	VacancyTopK     int

	// Optional overrides; zero values mean the Default* constants above.
	AITimeout      time.Duration
	VacancyTimeout time.Duration
	MaxRetries     int
}

// AISteps returns the ordered agent-backed batch chain:
//
//	interview (no-op, advances the stage) → analyst → synthesizer → review/save
//
// It returns nil when any dependency is missing, which is the signal for
// NewBatchPlan to keep the vacancy fallback instead of inserting a step that
// would fail every run.
func AISteps(analystClient llm.ChatClient, synth Synthesizer, store AITreeStore, analystPromptVersion string) []domain.Step {
	if analystClient == nil || synth == nil || store == nil {
		return nil
	}
	analysisStep := &AgentAnalysisStep{Client: analystClient, PromptVersion: analystPromptVersion}
	if loader, ok := store.(RunSourceLoader); ok {
		analysisStep.RunSources = loader
	}
	if loader, ok := store.(GlobalSourceLoader); ok {
		analysisStep.GlobalSources = loader
	}
	return []domain.Step{
		&InterviewStep{},
		analysisStep,
		&AgentSynthesisStep{Synthesizer: synth},
		&SaveAITreeStep{Store: store},
	}
}

// VacancySteps returns the historical vacancy-driven chain. It is the batch
// fallback used when no LLM wiring or no transcript is available.
func VacancySteps(store TreeStorer, catalog *analysis.Catalog, profiles []analysis.CandidateProfile, topK int) []domain.Step {
	if topK <= 0 {
		topK = 10
	}
	return []domain.Step{
		&InterviewStep{},
		&AnalysisStep{Catalog: catalog},
		&SynthesisStep{},
		&ReviewStep{Profiles: profiles, TopK: topK, Store: store},
	}
}

// NewBatchPlan builds the shared batch plan. The AI chain is returned only when
// the analyst client, the synthesizer and the tree store are all available;
// otherwise the old vacancy steps are kept, so a server without an LLM provider
// behaves exactly as before.
func NewBatchPlan(opts BatchOptions) BatchPlan {
	aiTimeout := opts.AITimeout
	if aiTimeout <= 0 {
		aiTimeout = DefaultAIStepTimeout
	}
	vacancyTimeout := opts.VacancyTimeout
	if vacancyTimeout <= 0 {
		vacancyTimeout = DefaultVacancyStepTimeout
	}
	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultBatchMaxRetries
	}

	plan := BatchPlan{
		Fallback:        VacancySteps(treeStorerOrNil(opts.Store), opts.VacancyCatalog, opts.VacancyProfiles, opts.VacancyTopK),
		AITimeout:       aiTimeout,
		FallbackTimeout: vacancyTimeout,
		MaxRetries:      maxRetries,
	}
	plan.AI = AISteps(opts.Analyst, opts.Synthesizer, opts.Store, opts.AnalystPromptVersion)
	if opts.ZunEnricher != nil {
		for _, step := range plan.AI {
			if synthesis, ok := step.(*AgentSynthesisStep); ok {
				synthesis.ZunEnricher = opts.ZunEnricher
			}
		}
	}
	return plan
}

// treeStorerOrNil converts a nil AITreeStore into a nil TreeStorer so the
// vacancy review step never receives a typed-but-empty interface.
func treeStorerOrNil(store AITreeStore) TreeStorer {
	if store == nil {
		return nil
	}
	return store
}
