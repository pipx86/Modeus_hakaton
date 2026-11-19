package main

import (
	"context"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

// newHybridFinalizer keeps the normal tree pipeline for interview runs and
// turns a learning run into a saved learning plan after its interview.
func newHybridFinalizer(pgStore *api.PGStore, treeFinalizer chat.Finalizer, planner *learningPlannerAdapter) chat.Finalizer {
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
