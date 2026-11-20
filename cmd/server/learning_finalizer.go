package main

import (
	"context"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/learning"
)

// learningFinalizeStore is the part of the PostgreSQL store needed to finish a
// learning run. The interface keeps the finalizer testable without a database.
type learningFinalizeStore interface {
	GetRun(runID string) (*domain.Run, error)
	SetStage(runID string, stage domain.Stage) error
	SaveLearningPlan(runID string, plan learning.Plan) error
}

type learningPlanRunner interface {
	PlanFromTranscript(ctx context.Context, profession, transcript string) (learning.Plan, error)
}

// newHybridFinalizer keeps the normal tree pipeline for interview runs and
// turns a learning run into a saved learning plan after its interview.
func newHybridFinalizer(pgStore learningFinalizeStore, treeFinalizer chat.Finalizer, planner learningPlanRunner) chat.Finalizer {
	if planner == nil {
		return treeFinalizer
	}
	return func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		run, err := pgStore.GetRun(runID)
		if err != nil {
			return domain.RunFailed, err
		}
		if run.Type != domain.RunTypeLearning {
			return treeFinalizer(ctx, runID, profession, session)
		}

		transcript := transcriptFromSession(session)
		plan, err := planner.PlanFromTranscript(ctx, profession, transcript)
		if err != nil {
			_ = pgStore.SetStage(runID, domain.StageFailed)
			return domain.RunFailed, err
		}
		if err := pgStore.SaveLearningPlan(runID, plan); err != nil {
			_ = pgStore.SetStage(runID, domain.StageFailed)
			return domain.RunFailed, err
		}
		if err := pgStore.SetStage(runID, domain.StageDone); err != nil {
			return domain.RunFailed, err
		}
		return domain.RunDone, nil
	}
}
