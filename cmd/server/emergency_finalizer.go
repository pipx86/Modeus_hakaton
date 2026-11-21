package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/emergency"
)

const emergencyReasonPrefix = "Модель анализа недоступна после нескольких ошибок. Показан детерминированный демонстрационный результат по ответам интервью. Его нужно проверить или повторить реальный анализ."

type emergencyFinalizerStore interface {
	finalizeStore
	SetDegradedReason(runID, reason string) error
}

// emergencyPolicy controls how many times the real analyst pipeline is tried
// before the deterministic tree is used. It is read from the environment so a
// deployment can tune it without a rebuild.
type emergencyPolicy struct {
	maxAttempts int
	retryDelay  time.Duration
}

func emergencyPolicyFromEnv() emergencyPolicy {
	policy := emergencyPolicy{maxAttempts: 2}
	if raw := strings.TrimSpace(os.Getenv("EMERGENCY_ANALYST_FAILURES")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 1 && value <= 5 {
			policy.maxAttempts = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("EMERGENCY_RETRY_DELAY_MS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 && value <= 60_000 {
			policy.retryDelay = time.Duration(value) * time.Millisecond
		}
	}
	return policy
}

// newEmergencyFinalizer wraps the normal finalizer. The primary pipeline is
// attempted up to EMERGENCY_ANALYST_FAILURES times (default 2); only after the
// second/configured failure does it save a deterministic tree from the real
// answers and return degraded. The UI must show the reason and offer a retry.
func newEmergencyFinalizer(primary chat.Finalizer, store emergencyFinalizerStore) chat.Finalizer {
	if primary == nil || store == nil {
		return primary
	}
	policy := emergencyPolicyFromEnv()
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		attempts := 0
		var status domain.RunStatus
		var err error
		for attempts < policy.maxAttempts {
			attempts++
			status, err = primary(ctx, runID, profession, session)
			if err == nil && status != domain.RunFailed {
				return status, nil
			}
			log.Printf("emergency: analyst attempt %d/%d failed for run %s: status=%s err=%v",
				attempts, policy.maxAttempts, runID, status, err)
			if attempts < policy.maxAttempts && policy.retryDelay > 0 {
				select {
				case <-ctx.Done():
					attempts = policy.maxAttempts
				case <-time.After(policy.retryDelay):
				}
			}
		}

		log.Printf("emergency: analyst failed %d times for run %s; using deterministic fallback", attempts, runID)
		nodes := emergency.Tree(profession, session)
		transcript := transcriptFromSession(session)
		if saveErr := saveInterviewTree(ctx, store, runID, profession, transcript, nodes, nil); saveErr != nil {
			log.Printf("emergency: save fallback tree for run %s: %v", runID, saveErr)
			if err != nil {
				return domain.RunFailed, err
			}
			return domain.RunFailed, fmt.Errorf("emergency: save fallback tree: %w", saveErr)
		}
		reason := fmt.Sprintf("%s Попыток анализа: %d.", emergencyReasonPrefix, attempts)
		if setErr := store.SetDegradedReason(runID, reason); setErr != nil {
			log.Printf("emergency: set degraded reason for run %s: %v", runID, setErr)
		}
		if stageErr := store.SetStage(runID, domain.StageDone); stageErr != nil {
			log.Printf("emergency: set done stage for run %s: %v", runID, stageErr)
		}
		return domain.RunDegraded, nil
	}
}
