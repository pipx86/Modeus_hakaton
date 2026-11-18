package steps

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// Data keys used by the AI batch steps. run.Data is ephemeral: it exists only
// for the lifetime of one Orchestrate call, so a resumed run rebuilds it from
// the persisted transcript before the steps are executed.
const (
	DataKeyTranscript        = "transcript"
	DataKeyInterviewNodes    = "interview_nodes"
	DataKeyTree              = "tree"
	DataKeyConflicts         = "conflicts"
	DataKeyInterviewSourceID = "interview_source_id"
)

// Synthesizer is the deterministic merge step of the agent pipeline. The
// production implementation is *synthesizer.Agent; tests inject fakes.
type Synthesizer interface {
	Synthesize(ctx context.Context, in agent.SynthesizerInput) (agent.SynthesizerOutput, error)
}

// AgentAnalysisStep runs the LLM analyst on the interview transcript stored in
// run.Data[DataKeyTranscript]. It puts evidence-backed nodes into
// run.Data[DataKeyInterviewNodes] for AgentSynthesisStep.
type AgentAnalysisStep struct {
	Client llm.ChatClient
	// PromptVersion pins the analyst prompt version (empty means the default v1).
	PromptVersion string
}

// Name implements domain.Step.
func (s *AgentAnalysisStep) Name() domain.Stage { return domain.StageAnalysis }

// Execute implements domain.Step. Batch analysis needs the transcript, so the
// data-aware path must be used.
func (s *AgentAnalysisStep) Execute(ctx context.Context, run *domain.Run) error {
	return s.ExecuteWithData(ctx, run, nil)
}

// ExecuteWithData implements domain.DataStep. Vacancies are not used: the
// transcript is the primary source for an interview-backed batch run.
func (s *AgentAnalysisStep) ExecuteWithData(ctx context.Context, run *domain.Run, _ []domain.VacancyRecord) error {
	if run == nil {
		return fmt.Errorf("agent analysis step: run is nil")
	}
	if s.Client == nil {
		return fmt.Errorf("agent analysis step: chat client is required")
	}
	transcript, _ := run.Data[DataKeyTranscript].(string)
	if strings.TrimSpace(transcript) == "" {
		return fmt.Errorf("agent analysis step: run.Data[%s] is empty", DataKeyTranscript)
	}

	options := []analyst.Option{}
	if s.PromptVersion != "" {
		options = append(options, analyst.WithPromptVersion(s.PromptVersion))
	}
	agentInstance, err := analyst.New(s.Client, options...)
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
	run.Data[DataKeyInterviewNodes] = nodes
	return nil
}

// AgentSynthesisStep merges interview nodes into one tree and stores the result
// in run.Data[DataKeyTree] and run.Data[DataKeyConflicts].
type AgentSynthesisStep struct {
	// Synthesizer is the deterministic merge agent. A nil value falls back to
	// the default synthesizer, which keeps direct step construction working.
	Synthesizer Synthesizer
}

// Name implements domain.Step.
func (s *AgentSynthesisStep) Name() domain.Stage { return domain.StageSynthesis }

// Execute implements domain.Step.
func (s *AgentSynthesisStep) Execute(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return fmt.Errorf("agent synthesis step: run is nil")
	}
	nodes, ok := run.Data[DataKeyInterviewNodes].([]domain.Node)
	if !ok || len(nodes) == 0 {
		return fmt.Errorf("agent synthesis step: run.Data[%s] is empty", DataKeyInterviewNodes)
	}
	synth := s.Synthesizer
	if synth == nil {
		agentInstance, err := synthesizer.New()
		if err != nil {
			return fmt.Errorf("agent synthesis step: synthesizer: %w", err)
		}
		synth = agentInstance
	}
	out, err := synth.Synthesize(ctx, agent.SynthesizerInput{
		Profession: run.Profession,
		Interview:  nodes,
	})
	if err != nil {
		return fmt.Errorf("agent synthesis step: synthesize: %w", err)
	}
	if run.Data == nil {
		run.Data = make(map[string]any)
	}
	run.Data[DataKeyTree] = out.Tree
	run.Data[DataKeyConflicts] = out.Conflicts
	return nil
}

// AITreeStore is the persistence surface of the AI batch review step: the tree
// writer plus the writer of the run's interview transcript source.
// *api.PGStore implements it in production.
type AITreeStore interface {
	TreeStorer
	CreateInterviewSource(ctx context.Context, runID, sourceID, transcript string) error
}

// SaveAITreeStep persists the tree produced by AgentSynthesisStep.
//
// It mirrors the chat finalizer persistence: the interview transcript becomes a
// source of the run, the model-provided node/evidence ids are replaced with
// database-safe UUIDs, every evidence item points at the interview source, and
// the tree is validated to have exactly one root before Store.SaveTree.
// A transcript source created earlier (and its id stored in
// run.Data[DataKeyInterviewSourceID]) is reused instead of duplicated.
type SaveAITreeStep struct {
	Store AITreeStore
	// NewID overrides the UUID generator; tests inject deterministic ids.
	NewID func() string
}

// Name implements domain.Step.
func (s *SaveAITreeStep) Name() domain.Stage { return domain.StageReview }

// treeExistenceChecker is the optional capability used to make the review step
// idempotent for a resumed run whose tree was already persisted before the
// process stopped (PGStore implements it).
type treeExistenceChecker interface {
	TreeExists(ctx context.Context, runID string) (bool, error)
}

// Execute implements domain.Step.
func (s *SaveAITreeStep) Execute(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return fmt.Errorf("save AI tree step: run is nil")
	}
	nodes, ok := run.Data[DataKeyTree].([]domain.Node)
	if !ok || len(nodes) == 0 {
		return fmt.Errorf("save AI tree step: run.Data[%s] is empty", DataKeyTree)
	}
	if s.Store == nil {
		return fmt.Errorf("save AI tree step: tree store is required")
	}
	if existing, ok := s.Store.(treeExistenceChecker); ok {
		found, err := existing.TreeExists(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("save AI tree step: check existing tree: %w", err)
		}
		if found {
			return nil
		}
	}

	sourceID, _ := run.Data[DataKeyInterviewSourceID].(string)
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		transcript, _ := run.Data[DataKeyTranscript].(string)
		if strings.TrimSpace(transcript) == "" {
			return fmt.Errorf("save AI tree step: run.Data[%s] is empty", DataKeyTranscript)
		}
		sourceID = s.nextID()
		if err := s.Store.CreateInterviewSource(ctx, run.ID, sourceID, transcript); err != nil {
			return fmt.Errorf("save AI tree step: create interview source: %w", err)
		}
		if run.Data == nil {
			run.Data = make(map[string]any)
		}
		run.Data[DataKeyInterviewSourceID] = sourceID
	}

	repaired, err := domain.RepairTree(nodes)
	if err != nil {
		return fmt.Errorf("save AI tree step: repair tree: %w", err)
	}
	converted := assignAITreeUUIDs(repaired, sourceID, s.nextID)
	roots := 0
	for _, node := range converted {
		if node.ParentID == nil {
			roots++
		}
	}
	if roots != 1 {
		return fmt.Errorf("save AI tree step: tree must have exactly one root, got %d", roots)
	}
	if err := s.Store.SaveTree(ctx, run.ID, converted); err != nil {
		return fmt.Errorf("save AI tree step: save: %w", err)
	}
	return nil
}

func (s *SaveAITreeStep) nextID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return newUUIDString()
}

// assignAITreeUUIDs rewrites the analyst tree into the shape the database
// expects: UUID node ids, UUID evidence ids and the run's interview source id on
// every evidence item (the analyst emits a synthetic interview source id that is
// not a database row). Title, description, type, hierarchy and quotes survive.
func assignAITreeUUIDs(nodes []domain.Node, sourceID string, newID func() string) []domain.Node {
	ids := make(map[string]string, len(nodes))
	for _, node := range nodes {
		ids[node.ID] = newID()
	}
	out := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		converted := node
		converted.ID = ids[node.ID]
		if node.ParentID != nil {
			if parent, ok := ids[*node.ParentID]; ok {
				parentID := parent
				converted.ParentID = &parentID
			} else {
				converted.ParentID = nil
			}
		}
		converted.Evidence = make([]domain.Evidence, 0, len(node.Evidence))
		for _, evidence := range node.Evidence {
			converted.Evidence = append(converted.Evidence, domain.Evidence{
				ID:       newID(),
				SourceID: sourceID,
				RefID:    evidence.RefID,
				Quote:    evidence.Quote,
				Status:   evidence.Status,
			})
		}
		out = append(out, converted)
	}
	return out
}

// newUUIDString returns a random RFC 4122 version 4 UUID in canonical form.
func newUUIDString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
