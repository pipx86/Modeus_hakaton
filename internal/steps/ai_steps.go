package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// AgentAnalysisStep runs the LLM analyst on the interview transcript stored in
// run.Data["transcript"]. It replaces the vacancy-only AnalysisStep for chat
// runs and puts evidence-backed nodes into run.Data["interview_nodes"].
type AgentAnalysisStep struct {
	Client llm.ChatClient
}

// Name implements domain.Step.
func (s *AgentAnalysisStep) Name() domain.Stage { return domain.StageAnalysis }

// Execute implements domain.Step. Chat analysis needs the transcript, so the
// data-aware path must be used.
func (s *AgentAnalysisStep) Execute(ctx context.Context, run *domain.Run) error {
	return s.ExecuteWithData(ctx, run, nil)
}

// ExecuteWithData implements domain.DataStep. Vacancies are not used: the
// transcript is the primary source for a chat run.
func (s *AgentAnalysisStep) ExecuteWithData(ctx context.Context, run *domain.Run, _ []domain.VacancyRecord) error {
	if run == nil {
		return fmt.Errorf("agent analysis step: run is nil")
	}
	if s.Client == nil {
		return fmt.Errorf("agent analysis step: chat client is required")
	}
	transcript, _ := run.Data["transcript"].(string)
	if transcript == "" {
		return fmt.Errorf("agent analysis step: run.Data[transcript] is empty")
	}

	agentInstance, err := analyst.New(s.Client)
	if err != nil {
		return fmt.Errorf("agent analysis step: analyst: %w", err)
	}
	analysis, err := agentInstance.Analyze(ctx, agent.AnalystInput{
		SessionID:  run.ID,
		Profession: run.Profession,
		Transcript: transcript,
	})
	if err != nil {
		return fmt.Errorf("agent analysis step: analyze: %w", err)
	}

	nodes := make([]domain.Node, 0, len(analysis.Entities))
	for _, entity := range analysis.Entities {
		nodes = append(nodes, entity.Node)
	}
	if run.Data == nil {
		run.Data = make(map[string]any)
	}
	run.Data["interview_nodes"] = nodes
	return nil
}

// AgentSynthesisStep merges interview nodes into one tree and stores the result
// in run.Data["tree"] and run.Data["conflicts"].
type AgentSynthesisStep struct{}

// Name implements domain.Step.
func (s *AgentSynthesisStep) Name() domain.Stage { return domain.StageSynthesis }

// Execute implements domain.Step.
func (s *AgentSynthesisStep) Execute(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return fmt.Errorf("agent synthesis step: run is nil")
	}
	nodes, ok := run.Data["interview_nodes"].([]domain.Node)
	if !ok || len(nodes) == 0 {
		return fmt.Errorf("agent synthesis step: run.Data[interview_nodes] is empty")
	}
	agentInstance, err := synthesizer.New()
	if err != nil {
		return fmt.Errorf("agent synthesis step: synthesizer: %w", err)
	}
	out, err := agentInstance.Synthesize(ctx, agent.SynthesizerInput{
		Profession: run.Profession,
		Interview:  nodes,
	})
	if err != nil {
		return fmt.Errorf("agent synthesis step: synthesize: %w", err)
	}
	if run.Data == nil {
		run.Data = make(map[string]any)
	}
	run.Data["tree"] = out.Tree
	run.Data["conflicts"] = out.Conflicts
	return nil
}

// SaveAITreeStep persists the tree produced by AgentSynthesisStep.
type SaveAITreeStep struct {
	Store TreeStorer
}

// Name implements domain.Step.
func (s *SaveAITreeStep) Name() domain.Stage { return domain.StageReview }

// Execute implements domain.Step.
func (s *SaveAITreeStep) Execute(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return fmt.Errorf("save AI tree step: run is nil")
	}
	nodes, ok := run.Data["tree"].([]domain.Node)
	if !ok || len(nodes) == 0 {
		return fmt.Errorf("save AI tree step: run.Data[tree] is empty")
	}
	if s.Store == nil {
		return fmt.Errorf("save AI tree step: tree store is required")
	}
	if err := s.Store.SaveTree(ctx, run.ID, nodes); err != nil {
		return fmt.Errorf("save AI tree step: save: %w", err)
	}
	return nil
}
