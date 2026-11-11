package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/analyst"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/store"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// newTreeFinalizer builds the post-interview pipeline: analyst, synthesizer
// and persistence of the resulting tree. It is installed on the chat service
// only when an analyst client is configured.
func newTreeFinalizer(pgStore *api.PGStore, analystClient llm.ChatClient) chat.Finalizer {
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		transcript := transcriptFromSession(session)
		analystAgent, err := analyst.New(analystClient)
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: analyst: %w", err)
		}
		analysis, err := analystAgent.Analyze(ctx, agent.AnalystInput{
			SessionID:  session.ID,
			Profession: profession,
			Transcript: transcript,
		})
		if err != nil {
			return domain.RunFailed, fmt.Errorf("finalizer: analyze: %w", err)
		}

		interviewNodes := make([]domain.Node, 0, len(analysis.Entities))
		for _, entity := range analysis.Entities {
			interviewNodes = append(interviewNodes, entity.Node)
		}

		synthesizerAgent, err := synthesizer.New()
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
