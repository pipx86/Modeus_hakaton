package steps

import (
	"context"
	"fmt"
	"strings"

	"activity-descriptor/internal/domain"
)

// TranscriptStore is the persistence surface needed to make a batch transcript
// durable across process restarts. *api.PGStore implements it in production.
type TranscriptStore interface {
	ListRunSourceContents(ctx context.Context, runID string) ([]domain.Source, error)
	CreateInterviewSource(ctx context.Context, runID, sourceID, transcript string) error
	LinkRunSources(ctx context.Context, runID string, sourceIDs []string) error
}

// SetTranscript stores the batch transcript and the id of its persisted source
// in the ephemeral run data consumed by the AI steps.
func SetTranscript(run *domain.Run, transcript, sourceID string) {
	if run == nil {
		return
	}
	if run.Data == nil {
		run.Data = make(map[string]any)
	}
	run.Data[DataKeyTranscript] = transcript
	if sourceID != "" {
		run.Data[DataKeyInterviewSourceID] = sourceID
	}
}

// PersistInterviewTranscript saves the provided transcript as an interview
// source bound to the run and links it through run_sources, so the resume
// processor can find the same transcript after a restart. It returns the id of
// the created source.
//
// The id is generated here (not by the database) because SaveAITreeStep may need
// the same id for evidence source references; the caller stores it in run.Data.
func PersistInterviewTranscript(ctx context.Context, store TranscriptStore, runID, transcript string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("persist transcript: transcript store is required")
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return "", fmt.Errorf("persist transcript: transcript is empty")
	}
	if strings.TrimSpace(runID) == "" {
		return "", fmt.Errorf("persist transcript: run id is empty")
	}
	sourceID := newUUIDString()
	if err := store.CreateInterviewSource(ctx, runID, sourceID, transcript); err != nil {
		return "", fmt.Errorf("persist transcript: create interview source: %w", err)
	}
	if err := store.LinkRunSources(ctx, runID, []string{sourceID}); err != nil {
		return "", fmt.Errorf("persist transcript: link interview source: %w", err)
	}
	return sourceID, nil
}

// LoadInterviewTranscript returns the interview transcript persisted for a run
// together with the id of its source. ok is false when the run has no interview
// source yet (for example a vacancy-only run created before the transcript
// input existed).
func LoadInterviewTranscript(ctx context.Context, store TranscriptStore, runID string) (transcript, sourceID string, ok bool, err error) {
	if store == nil {
		return "", "", false, nil
	}
	sources, err := store.ListRunSourceContents(ctx, runID)
	if err != nil {
		return "", "", false, fmt.Errorf("load transcript: %w", err)
	}
	for _, source := range sources {
		if source.Type != domain.SourceInterview {
			continue
		}
		if strings.TrimSpace(source.Content) == "" {
			continue
		}
		return source.Content, source.ID, true, nil
	}
	return "", "", false, nil
}
