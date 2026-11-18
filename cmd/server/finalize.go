package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/search"

	"github.com/jackc/pgx/v5/pgtype"
)

// sourceRetriever returns only the source fragments relevant to a run query.
type sourceRetriever func(ctx context.Context, runID, query string) ([]domain.Source, error)

// finalizeStore is the slice of the persistence layer the post-interview
// pipeline needs. *api.PGStore implements it in production; tests inject a fake
// so the finalizer can be verified without PostgreSQL or a live LLM.
type finalizeStore interface {
	SetStage(id string, stage domain.Stage) error
	ListRunSources(ctx context.Context, runID string) ([]domain.Source, error)
	CreateInterviewSource(ctx context.Context, runID, sourceID, transcript string) error
	SaveTree(ctx context.Context, id string, nodes []domain.Node) error
	SaveRunSnapshot(ctx context.Context, runID string) error
	SaveSynthesisReview(ctx context.Context, runID string, conflicts []domain.Conflict, messages []agent.AgentMessage) error
}

// interviewAnalyst is the analyst step of the pipeline. It is an interface so a
// fake can stand in for the LLM-backed agent in tests.
type interviewAnalyst interface {
	Analyze(ctx context.Context, in agent.AnalystInput) (agent.AnalystOutput, error)
}

// synthesisRunner is the deterministic merge step of the pipeline.
type synthesisRunner interface {
	Synthesize(ctx context.Context, in agent.SynthesizerInput) (agent.SynthesizerOutput, error)
}

// finalizerDeps are the collaborators of one finalization. zunClient is
// optional and only used for the best-effort knowledge/skills enrichment.
type finalizerDeps struct {
	store       finalizeStore
	analyst     interviewAnalyst
	synthesizer synthesisRunner
	zunClient   llm.ChatClient
	retrievers  []sourceRetriever
}

// newTreeFinalizer builds the post-interview pipeline: analyst, synthesizer
// and persistence of the resulting tree. An optional retriever injects RAG
// context instead of sending every source to the model.
func newTreeFinalizer(store finalizeStore, analystClient llm.ChatClient, retrievers ...sourceRetriever) chat.Finalizer {
	return newTreeFinalizerWithVersions(store, analystClient, "", "", retrievers...)
}

func newTreeFinalizerWithVersions(store finalizeStore, analystClient llm.ChatClient, analystVersion, synthesizerVersion string, retrievers ...sourceRetriever) chat.Finalizer {
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		analystOptions := []analyst.Option{}
		if analystVersion != "" {
			analystOptions = append(analystOptions, analyst.WithPromptVersion(analystVersion))
		}
		analystAgent, err := analyst.New(analystClient, analystOptions...)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: analyst: %w", err)
		}
		synthesizerOptions := []synthesizer.Option{}
		if synthesizerVersion != "" {
			synthesizerOptions = append(synthesizerOptions, synthesizer.WithPromptVersion(synthesizerVersion))
		}
		synthesizerAgent, err := synthesizer.New(synthesizerOptions...)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: synthesizer: %w", err)
		}
		return finalizeInterview(ctx, finalizerDeps{
			store:       store,
			analyst:     analystAgent,
			synthesizer: synthesizerAgent,
			zunClient:   analystClient,
			retrievers:  retrievers,
		}, runID, profession, session)
	}
}

// finalizeInterview runs analysis, deterministic synthesis, persistence of the
// review artifacts and the final tree for one finished interview.
func finalizeInterview(ctx context.Context, deps finalizerDeps, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
	if deps.store == nil {
		return domain.RunFailed, fmt.Errorf("finalizer: store is required")
	}
	transcript := transcriptFromSession(session)
	setFinalizeStage(deps.store, runID, domain.StageAnalysis)

	input := agent.AnalystInput{
		SessionID:  session.ID,
		Profession: profession,
		Transcript: transcript,
	}
	if len(deps.retrievers) > 0 && deps.retrievers[0] != nil {
		query := profession + " " + truncateRunes(transcript, 2000)
		if sources, retrieveErr := deps.retrievers[0](ctx, runID, query); retrieveErr == nil {
			input.Sources = sources
		}
	}
	analysis, err := deps.analyst.Analyze(ctx, input)
	if err != nil {
		return domain.RunFailed, fmt.Errorf("finalizer: analyze: %w", err)
	}

	interviewNodes := make([]domain.Node, 0, len(analysis.Entities))
	for _, entity := range analysis.Entities {
		interviewNodes = append(interviewNodes, entity.Node)
	}

	// The real sources linked to this run are the only non-interview data that
	// exists at finalization time. Loading them is best-effort: without extra
	// sources the already supported interview-only path must keep working.
	runSources, err := deps.store.ListRunSources(ctx, runID)
	if err != nil {
		log.Printf("finalizer: load run sources for %s: %v", runID, err)
		runSources = nil
	}

	setFinalizeStage(deps.store, runID, domain.StageSynthesis)
	synthesis, err := deps.synthesizer.Synthesize(ctx, buildSynthesizerInput(profession, runSources, interviewNodes))
	if err != nil {
		return domain.RunFailed, fmt.Errorf("finalizer: synthesize: %w", err)
	}

	// Persist conflicts/messages before the tree so the evidence for expert
	// review is not lost if tree persistence fails afterwards. Review storage is
	// auxiliary: a failure is logged but does not fail an otherwise valid run,
	// same as the automatic snapshot below.
	if persistErr := deps.store.SaveSynthesisReview(ctx, runID, synthesis.Conflicts, synthesis.Messages); persistErr != nil {
		log.Printf("finalizer: persist synthesis review for run %s: %v", runID, persistErr)
	}

	setFinalizeStage(deps.store, runID, domain.StageReview)
	nodes := enrichZun(ctx, deps.zunClient, profession, synthesis.Tree)
	if err := saveInterviewTree(ctx, deps.store, runID, profession, transcript, nodes); err != nil {
		return domain.RunFailed, fmt.Errorf("finalizer: save tree: %w", err)
	}
	setFinalizeStage(deps.store, runID, domain.StageDone)
	return domain.RunDone, nil
}

// buildSynthesizerInput assembles the deterministic merge input.
//
// Only interview nodes exist in production today: there is no extraction
// pipeline that turns raw vacancy/standard sources into domain.Node records, so
// Vacancy and Standard are deliberately left empty instead of inventing nodes.
// The run's real sources are still passed as metadata so the merge resolves
// source ids and trust priorities for any future non-interview nodes.
//
// TODO(synthesis): when standard/vacancy entity extraction exists, pass those
// real nodes into SynthesizerInput.Standard/Vacancy instead of leaving them empty.
func buildSynthesizerInput(profession string, sources []domain.Source, interviewNodes []domain.Node) agent.SynthesizerInput {
	return agent.SynthesizerInput{
		Profession: profession,
		Sources:    sources,
		Interview:  interviewNodes,
	}
}

// setFinalizeStage обновляет этап прогона, чтобы UI видел прогресс пайплайна.
func setFinalizeStage(store finalizeStore, runID string, stage domain.Stage) {
	if store == nil {
		return
	}
	_ = store.SetStage(runID, stage)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// newRAGRetriever builds a lexical/vector retriever. It degrades to BM25 when
// Ollama is unavailable; an empty index simply returns no sources.
func newRAGRetriever(pgStore *api.PGStore) sourceRetriever {
	if pgStore == nil || pgStore.Pool == nil {
		return nil
	}
	address := os.Getenv("OLLAMA_URL")
	model := os.Getenv("OLLAMA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	service := search.Service{
		Pool:  pgStore.Pool,
		Model: embeddings.Ollama{BaseURL: address, Model: model},
	}
	return func(ctx context.Context, runID, query string) ([]domain.Source, error) {
		var id pgtype.UUID
		if err := id.Scan(runID); err != nil || !id.Valid {
			return nil, fmt.Errorf("invalid run id: %w", err)
		}
		hits, _, err := service.Search(ctx, id, query, 8)
		if err != nil {
			return nil, err
		}
		bySource := make(map[string]*domain.Source)
		for _, hit := range hits {
			sourceID := hit.SourceID.String()
			item := bySource[sourceID]
			if item == nil {
				item = &domain.Source{ID: sourceID, Type: domain.SourceDocument, Title: "Retrieved source"}
				bySource[sourceID] = item
			}
			if item.Content != "" {
				item.Content += "\n"
			}
			item.Content += hit.Quote
		}
		out := make([]domain.Source, 0, len(bySource))
		for _, item := range bySource {
			out = append(out, *item)
		}
		return out, nil
	}
}

func transcriptFromSession(session domain.InterviewSession) string {
	var b strings.Builder
	for i, question := range session.Questions {
		if i >= len(session.Answers) {
			break
		}
		b.WriteString("Вопрос: ")
		b.WriteString(question.Text)
		b.WriteString("\nОтвет: ")
		b.WriteString(session.Answers[i].Text)
		b.WriteString("\n")
	}
	return b.String()
}

// saveInterviewTree assigns database-safe UUIDs to the AI tree, creates the
// interview source and persists the tree through the finalize store.
func saveInterviewTree(ctx context.Context, store finalizeStore, runID, profession, transcript string, nodes []domain.Node) error {
	if store == nil {
		return fmt.Errorf("postgres store is not configured")
	}
	sourceID := newUUID()
	if err := store.CreateInterviewSource(ctx, runID, sourceID.String(), transcript); err != nil {
		return fmt.Errorf("create interview source: %w", err)
	}

	repaired, err := domain.RepairTree(nodes)
	if err != nil {
		return fmt.Errorf("repair tree: %w", err)
	}
	converted := assignUUIDs(repaired, sourceID)
	rootCount := 0
	for _, node := range converted {
		if node.ParentID == nil {
			rootCount++
		}
	}
	if rootCount != 1 {
		return fmt.Errorf("tree must have exactly one root, got %d", rootCount)
	}
	if err := store.SaveTree(ctx, runID, converted); err != nil {
		return fmt.Errorf("store tree: %w", err)
	}
	// Автоснимок дерева: вкладка «Снимки и проверка» получает базовую точку
	// для diff, без ручного POST /snapshots.
	if snapErr := store.SaveRunSnapshot(ctx, runID); snapErr != nil {
		log.Printf("chat: snapshot failed for run %s: %v", runID, snapErr)
	}
	return nil
}

func assignUUIDs(nodes []domain.Node, sourceID pgtype.UUID) []domain.Node {
	ids := make(map[string]string, len(nodes))
	for _, node := range nodes {
		ids[node.ID] = newUUID().String()
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
				ID:       newUUID().String(),
				SourceID: sourceID.String(),
				RefID:    evidence.RefID,
				Quote:    evidence.Quote,
			})
		}
		out = append(out, converted)
	}
	return out
}

func newUUID() pgtype.UUID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return pgtype.UUID{Bytes: b, Valid: true}
}
