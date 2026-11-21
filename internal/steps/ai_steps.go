package steps

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llmobs"
	"activity-descriptor/internal/sourcecluster"
)

// Data keys used by the AI batch steps. run.Data is ephemeral: it exists only
// for the lifetime of one Orchestrate call, so a resumed run rebuilds it from
// the persisted transcript before the steps are executed.
const (
	DataKeyTranscript     = "transcript"
	DataKeyInterviewNodes = "interview_nodes"
	DataKeyTree           = "tree"
	DataKeyConflicts      = "conflicts"
	DataKeyMessages       = "agent_messages"
	// DataKeySourceMetadata holds the declared sources (id, type, title,
	// priority) the analyst was given, without their content. The synthesis step
	// needs it to route every node to the slot its evidence proves and to
	// attribute a conflict value to the material it came from.
	DataKeySourceMetadata    = "source_metadata"
	DataKeyInterviewSourceID = "interview_source_id"
	DataKeySourceIDs         = "source_ids"
)

// Synthesizer is the synthesis step of the agent pipeline: it merges the
// interview nodes deterministically and, when its LLM client is configured,
// normalizes the human-readable titles/descriptions. The production
// implementation is *synthesizer.Agent; tests inject fakes.
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
	// RunSources and GlobalSources are optional best-effort loaders for the
	// materials that must be visible to the analyst alongside the transcript.
	RunSources    RunSourceLoader
	GlobalSources GlobalSourceLoader
}

// RunSourceLoader returns the sources linked to one run.
type RunSourceLoader interface {
	ListRunSourceContents(ctx context.Context, runID string) ([]domain.Source, error)
}

// GlobalSourceLoader returns admin-uploaded materials shared by all runs.
type GlobalSourceLoader interface {
	ListGlobalSources(limit int) ([]domain.Source, error)
}

// Name implements domain.Step.
func (s *AgentAnalysisStep) Name() domain.Stage { return domain.StageAnalysis }

// Execute implements domain.Step. Batch analysis needs the transcript, so the
// data-aware path must be used.
func (s *AgentAnalysisStep) Execute(ctx context.Context, run *domain.Run) error {
	return s.ExecuteWithData(ctx, run, nil)
}

// ExecuteWithData implements domain.DataStep. The transcript is the primary
// input; linked run sources, admin materials and profession vacancies are added
// as supporting evidence when loaders are configured.
func (s *AgentAnalysisStep) ExecuteWithData(ctx context.Context, run *domain.Run, vacancies []domain.VacancyRecord) error {
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
	// Attribute every analyst LLM request to this batch run, so
	// llm_requests.run_id is populated for the run cost endpoint instead of
	// depending on a time-window guess.
	ctx = llmobs.WithRunID(ctx, run.ID)
	sources := s.analysisSources(ctx, run.ID, run.Profession, vacancies)
	analysis, err := agentInstance.Analyze(ctx, agent.AnalystInput{
		SessionID:  run.ID,
		Profession: run.Profession,
		Transcript: transcript,
		Sources:    sources,
	})
	if err != nil {
		return fmt.Errorf("agent analysis step: analyze: %w", err)
	}
	if run.Data == nil {
		run.Data = make(map[string]any)
	}
	sourceIDs := make(map[string]string)
	for _, source := range sources {
		if source.ID != "" {
			sourceIDs[source.ID] = source.ID
		}
	}
	run.Data[DataKeySourceIDs] = sourceIDs
	// Keep the declared provenance of every material the analyst could quote so
	// the synthesis step routes the resulting nodes into the same
	// interview/standard/vacancy slots as the chat finalizer.
	run.Data[DataKeySourceMetadata] = sourceMetadata(sources)

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

// analysisSources merges admin materials, linked run sources and vacancies,
// then keeps only the profession cluster. Explicitly linked run sources are
// trusted; global materials and HH vacancies must mention the profession.
// Duplicate ids are collapsed; the interview transcript is not repeated because
// the analyst receives it in a dedicated field.
func (s *AgentAnalysisStep) analysisSources(ctx context.Context, runID, profession string, vacancies []domain.VacancyRecord) []domain.Source {
	sources := make([]domain.Source, 0, len(vacancies)+8)
	explicit := make(map[string]bool)
	if s.GlobalSources != nil {
		if global, err := s.GlobalSources.ListGlobalSources(50); err == nil {
			sources = append(sources, global...)
		}
	}
	if s.RunSources != nil {
		if linked, err := s.RunSources.ListRunSourceContents(ctx, runID); err == nil {
			for _, source := range linked {
				if source.ID != "" {
					explicit[source.ID] = true
				}
			}
			sources = append(sources, linked...)
		}
	}
	for _, vacancy := range vacancies {
		if strings.TrimSpace(vacancy.SourceID) == "" || strings.TrimSpace(vacancy.Description) == "" {
			continue
		}
		if s.RunSources == nil && s.GlobalSources == nil {
			// A bare step without source loaders keeps the historical
			// transcript-only behaviour used by direct callers and tests.
			break
		}
		sources = append(sources, domain.Source{
			ID:        vacancy.SourceID,
			Type:      domain.SourceVacancy,
			Title:     vacancy.Title,
			Content:   vacancy.Description,
			SourceURL: vacancy.SourceURL,
		})
	}

	clustered := sourcecluster.Select("", sources, explicit)
	if clustered.Excluded > 0 {
		log.Printf("steps: excluded %d sources outside profession %q for run %s", clustered.Excluded, profession, runID)
	}
	return clustered.Sources
}

// sourceMetadata returns the declared sources without their content: synthesis
// only needs ids, types, titles and priorities to route evidence into the right
// slot and to name the material a conflict value came from. Keeping content out
// of run.Data avoids holding every document twice for the run.
func sourceMetadata(sources []domain.Source) []domain.Source {
	out := make([]domain.Source, 0, len(sources))
	for _, source := range sources {
		out = append(out, domain.Source{
			ID:       source.ID,
			Type:     source.Type,
			Title:    source.Title,
			Priority: source.EffectivePriority(),
		})
	}
	return out
}

// AgentSynthesisStep merges the analyst nodes into one tree and stores the result
// in run.Data[DataKeyTree], run.Data[DataKeyConflicts] and
// run.Data[DataKeyMessages].
//
// The nodes are first routed into the interview/standard/vacancy input slots by
// the provenance of their evidence, exactly like the chat finalizer does. Without
// that routing every node would sit in the Interview slot, the deterministic
// merge would never see two sources disagree on one element and a batch run would
// silently keep one of the values instead of reporting the conflict
// (карточка 9 плана MVP).
type AgentSynthesisStep struct {
	// Synthesizer is the hybrid synthesis agent: the deterministic merge always
	// runs, and the optional LLM only normalizes titles/descriptions. A nil
	// value falls back to the default synthesizer, which keeps direct step
	// construction working.
	Synthesizer Synthesizer
	// ZunEnricher optionally fills knowledge/skills/abilities after synthesis.
	ZunEnricher ZunEnricher
}

// ZunEnricher adds ZUN to a synthesized tree. It is best-effort: the caller
// decides whether an enrichment failure should fail the run.
type ZunEnricher func(ctx context.Context, profession string, nodes []domain.Node) []domain.Node

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
	// The declared sources carry the ids, types and priorities the merge needs to
	// detect a cross-source disagreement and to name the material behind every
	// competing value. A step constructed without the analyst step (direct
	// callers, tests) simply has no metadata and keeps the interview-only path.
	sources, _ := run.Data[DataKeySourceMetadata].([]domain.Source)
	interviewNodes, standardNodes, vacancyNodes := PartitionNodesByProvenance(nodes, sources)
	synth := s.Synthesizer
	if synth == nil {
		agentInstance, err := synthesizer.New()
		if err != nil {
			return fmt.Errorf("agent synthesis step: synthesizer: %w", err)
		}
		synth = agentInstance
	}
	// Attribute the synthesizer's optional normalization call to this run, the
	// same way the analyst step does, so llm_requests.run_id is populated for
	// batch runs that now include a real synthesizer model call.
	ctx = llmobs.WithRunID(ctx, run.ID)
	out, err := synth.Synthesize(ctx, agent.SynthesizerInput{
		Profession: run.Profession,
		Sources:    sources,
		Interview:  interviewNodes,
		Standard:   standardNodes,
		Vacancy:    vacancyNodes,
	})
	if err != nil {
		return fmt.Errorf("agent synthesis step: synthesize: %w", err)
	}
	if run.Data == nil {
		run.Data = make(map[string]any)
	}
	if s.ZunEnricher != nil {
		out.Tree = s.ZunEnricher(ctx, run.Profession, out.Tree)
	}
	run.Data[DataKeyTree] = out.Tree
	run.Data[DataKeyConflicts] = out.Conflicts
	run.Data[DataKeyMessages] = out.Messages
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

// synthesisReviewStore is the optional capability that persists the resolved
// conflicts and the synthesizer's agent memory for expert review. *api.PGStore
// implements it through SaveSynthesisReview; a store without it keeps working
// because review storage is auxiliary.
type synthesisReviewStore interface {
	SaveSynthesisReview(ctx context.Context, runID string, conflicts []domain.Conflict, messages []agent.AgentMessage) error
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
	// Persist the review artifacts before the tree, exactly like the chat
	// finalizer: the expert must see the disagreements even if the tree write
	// fails afterwards, and GET /runs/{id}/conflicts has to return the same data
	// for a batch run as for a chat run (карточка 9 плана MVP).
	s.saveSynthesisReview(ctx, run)
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
	sourceIDs, _ := run.Data[DataKeySourceIDs].(map[string]string)
	converted, ids := assignAITreeUUIDsWithIDs(repaired, sourceID, sourceIDs, s.nextID)
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
	s.saveSynthesisReviewWithIDs(ctx, run, ids)
	return nil
}

func (s *SaveAITreeStep) nextID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return newUUIDString()
}

// saveSynthesisReview stores the conflicts and messages the synthesis step put
// into run.Data. The presence of DataKeyConflicts is what marks "this execution
// synthesized a tree": a step that never ran the synthesizer must not wipe
// previously stored review rows with an empty list. Storage failures are logged
// and never fail an otherwise valid run, same as in the chat finalizer.
func (s *SaveAITreeStep) saveSynthesisReview(ctx context.Context, run *domain.Run) {
	conflicts, synthesized := run.Data[DataKeyConflicts].([]domain.Conflict)
	if !synthesized {
		return
	}
	store, ok := s.Store.(synthesisReviewStore)
	if !ok {
		return
	}
	messages, _ := run.Data[DataKeyMessages].([]agent.AgentMessage)
	if err := store.SaveSynthesisReview(ctx, run.ID, conflicts, messages); err != nil {
		log.Printf("save AI tree step: persist synthesis review for run %s: %v", run.ID, err)
	}
}

// assignAITreeUUIDs rewrites the analyst tree into the shape the database
// expects: UUID node ids, UUID evidence ids and the run's interview source id on
// every evidence item (the analyst emits a synthetic interview source id that is
// not a database row). Title, description, type, hierarchy and quotes survive.
// assignAITreeUUIDs переписывает дерево аналитика в форму БД. Обёртка над
// assignAITreeUUIDsWithIDs для вызовов, которым карта id не нужна.
func assignAITreeUUIDs(nodes []domain.Node, sourceID string, sourceIDs map[string]string, newID func() string) []domain.Node {
	out, _ := assignAITreeUUIDsWithIDs(nodes, sourceID, sourceIDs, newID)
	return out
}

func assignAITreeUUIDsWithIDs(nodes []domain.Node, sourceID string, sourceIDs map[string]string, newID func() string) ([]domain.Node, map[string]string) {
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
			evidenceSourceID := sourceID
			if mapped, ok := sourceIDs[evidence.SourceID]; ok {
				evidenceSourceID = mapped
			}
			converted.Evidence = append(converted.Evidence, domain.Evidence{
				ID:       newID(),
				SourceID: evidenceSourceID,
				RefID:    evidence.RefID,
				Quote:    evidence.Quote,
				Status:   evidence.Status,
			})
		}
		out = append(out, converted)
	}
	return out, ids
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

// saveSynthesisReviewWithIDs переписывает review с реальными UUID узлов:
// конфликты сохраняются до дерева, чтобы не потерять разногласия при сбое записи.
func (s *SaveAITreeStep) saveSynthesisReviewWithIDs(ctx context.Context, run *domain.Run, ids map[string]string) {
	if len(ids) == 0 {
		return
	}
	conflicts, synthesized := run.Data[DataKeyConflicts].([]domain.Conflict)
	if !synthesized || len(conflicts) == 0 {
		return
	}
	store, ok := s.Store.(synthesisReviewStore)
	if !ok {
		return
	}
	messages, _ := run.Data[DataKeyMessages].([]agent.AgentMessage)
	remapped := RemapConflictNodeIDs(conflicts, ids)
	if err := store.SaveSynthesisReview(ctx, run.ID, remapped, messages); err != nil {
		log.Printf("save AI tree step: remap synthesis review for run %s: %v", run.ID, err)
	}
}
