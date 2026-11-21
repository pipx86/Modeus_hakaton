package main

import (
	"context"
	"fmt"
	"log"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/emergency"
)

const emergencyReason = "Модель анализа недоступна. Показан детерминированный демонстрационный результат по ответам интервью. Его нужно проверить или повторить реальный анализ."

type emergencyFinalizerStore interface {
	finalizeStore
	SetDegradedReason(runID, reason string) error
}

// newEmergencyFinalizer wraps the normal finalizer. If the model pipeline fails
// or returns a failed status, it saves a deterministic tree built from the
// respondent's answers and returns degraded, never failed. The UI must show
// emergencyReason and offer a retry.
func newEmergencyFinalizer(primary chat.Finalizer, store emergencyFinalizerStore) chat.Finalizer {
	if primary == nil || store == nil {
		return primary
	}
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		status, err := primary(ctx, runID, profession, session)
		if err == nil && status != domain.RunFailed {
			return status, nil
		}
		log.Printf("emergency: primary finalizer failed for run %s: status=%s err=%v; using deterministic fallback", runID, status, err)

		nodes := emergency.Tree(profession, session)
		transcript := transcriptFromSession(session)
		if saveErr := saveInterviewTree(ctx, store, runID, profession, transcript, nodes, nil); saveErr != nil {
			log.Printf("emergency: save fallback tree for run %s: %v", runID, saveErr)
			if err != nil {
				return domain.RunFailed, err
			}
			return domain.RunFailed, fmt.Errorf("emergency: save fallback tree: %w", saveErr)
		}
		if setErr := store.SetDegradedReason(runID, emergencyReason); setErr != nil {
			log.Printf("emergency: set degraded reason for run %s: %v", runID, setErr)
		}
		if stageErr := store.SetStage(runID, domain.StageDone); stageErr != nil {
			log.Printf("emergency: set done stage for run %s: %v", runID, stageErr)
		}
		return domain.RunDegraded, nil
	}
}
