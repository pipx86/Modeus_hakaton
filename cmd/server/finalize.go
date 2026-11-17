package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/embeddings"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/search"
	"activity-descriptor/internal/store"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// sourceRetriever returns only the source fragments relevant to a run query.
type sourceRetriever func(ctx context.Context, runID, query string) ([]domain.Source, error)

// newTreeFinalizer builds the post-interview pipeline: analyst, synthesizer
// and persistence of the resulting tree. An optional retriever injects RAG
// context instead of sending every source to the model.
func newTreeFinalizer(pgStore *api.PGStore, analystClient llm.ChatClient, retrievers ...sourceRetriever) chat.Finalizer {
	return newTreeFinalizerWithVersions(pgStore, analystClient, "", "", retrievers...)
}

func newTreeFinalizerWithVersions(pgStore *api.PGStore, analystClient llm.ChatClient, analystVersion, synthesizerVersion string, retrievers ...sourceRetriever) chat.Finalizer {
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		transcript := transcriptFromSession(session)
		analystOptions := []analyst.Option{}
		if analystVersion != "" {
			analystOptions = append(analystOptions, analyst.WithPromptVersion(analystVersion))
		}
		analystAgent, err := analyst.New(analystClient, analystOptions...)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: analyst: %w", err)
		}
		input := agent.AnalystInput{
			SessionID:  session.ID,
			Profession: profession,
			Transcript: transcript,
		}
		if len(retrievers) > 0 && retrievers[0] != nil {
			query := profession + " " + truncateRunes(transcript, 2000)
			if sources, retrieveErr := retrievers[0](ctx, runID, query); retrieveErr == nil {
				input.Sources = sources
			}
		}
		analysis, err := analystAgent.Analyze(ctx, input)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: analyze: %w", err)
		}

		interviewNodes := make([]domain.Node, 0, len(analysis.Entities))
		for _, entity := range analysis.Entities {
			interviewNodes = append(interviewNodes, entity.Node)
		}

		synthesizerOptions := []synthesizer.Option{}
		if synthesizerVersion != "" {
			synthesizerOptions = append(synthesizerOptions, synthesizer.WithPromptVersion(synthesizerVersion))
		}
		synthesizerAgent, err := synthesizer.New(synthesizerOptions...)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: synthesizer: %w", err)
		}
		synthesis, err := synthesizerAgent.Synthesize(ctx, agent.SynthesizerInput{
			Profession: profession,
			Interview:  interviewNodes,
		})
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: synthesize: %w", err)
		}

		if err := saveInterviewTree(ctx, pgStore, runID, profession, transcript, synthesis.Tree); err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: save tree: %w", err)
		}
		return domain.RunDone, nil
	}
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
// interview source and persists the tree through the existing PGStore.
func saveInterviewTree(ctx context.Context, pgStore *api.PGStore, runID, profession, transcript string, nodes []domain.Node) error {
	if pgStore == nil || pgStore.Pool == nil {
		return fmt.Errorf("postgres store is not configured")
	}
	var runUUID pgtype.UUID
	if err := runUUID.Scan(runID); err != nil || !runUUID.Valid {
		return fmt.Errorf("invalid run id %q", runID)
	}

	sourceID := newUUID()
	now := time.Now().UTC()
	_, err := store.New(pgStore.Pool).CreateSource(ctx, db.CreateSourceParams{
		ID:               sourceID,
		RunID:            runUUID,
		Type:             string(domain.SourceInterview),
		Title:            "Interview transcript",
		Content:          pgtype.Text{String: transcript, Valid: true},
		RawPayload:       nil,
		ProcessingStatus: "processed",
		ProcessedAt:      pgtype.Timestamptz{Time: now, Valid: true},
		Provider:         pgtype.Text{String: "ai", Valid: true},
	})
	if err != nil {
		return fmt.Errorf("create interview source: %w", err)
	}

	converted := assignUUIDs(nodes, sourceID)
	rootCount := 0
	for _, node := range converted {
		if node.ParentID == nil {
			rootCount++
		}
	}
	if rootCount != 1 {
		return fmt.Errorf("tree must have exactly one root, got %d", rootCount)
	}
	if err := pgStore.SaveTree(ctx, runID, converted); err != nil {
		return fmt.Errorf("store tree: %w", err)
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
