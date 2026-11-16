package domain

import (
	"context"
	"fmt"
	"time"
)

type Step interface {
	Name() Stage
	Execute(ctx context.Context, run *Run) error
}

type DataStep interface {
	Step
	ExecuteWithData(ctx context.Context, run *Run, vacancies []VacancyRecord) error
}

type Store interface {
	Save(run *Run) error
}

func Orchestrate(
	ctx context.Context,
	run *Run,
	steps []Step,
	store Store,
	reader VacancyReader,
	maxRetries int,
	stepTO time.Duration,
) error {
	if run.Stage == StageDone || run.Stage == StageFailed {
		return nil
	}

	if run.Status == RunPending {
		run.Status = RunRunning
		if err := store.Save(run); err != nil {
			return fail(run, store, fmt.Errorf("save on start: %w", err))
		}
	}

	for _, st := range steps {
		target := st.Name()

		if isStageBeforeCurrent(run.Stage, target) {
			continue
		}

		if !CanTransition(run.Stage, target) {
			return fail(run, store, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, run.Stage, target))
		}

		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			stepCtx, cancel := context.WithTimeout(ctx, stepTO)

			if ds, ok := st.(DataStep); ok && reader != nil {
				vacs, err := reader.ByProfession(stepCtx, run.Profession, 100)
				if err != nil {
					lastErr = fmt.Errorf("read vacancies: %w", err)
				} else {
					lastErr = ds.ExecuteWithData(stepCtx, run, vacs)
				}
			} else {
				lastErr = st.Execute(stepCtx, run)
			}

			cancel()
			if lastErr == nil {
				break
			}
		}

		if lastErr != nil {
			return fail(run, store, fmt.Errorf("step %s failed after %d retries: %w", target, maxRetries, lastErr))
		}

		run.Stage = target
		if err := store.Save(run); err != nil {
			return fail(run, store, fmt.Errorf("save after %s: %w", target, err))
		}
	}

	if !CanTransition(run.Stage, StageDone) {
		return fail(run, store, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, run.Stage, StageDone))
	}
	run.Stage = StageDone
	run.Status = RunDone
	return store.Save(run)
}

func isStageBeforeCurrent(current, target Stage) bool {
	order := map[Stage]int{
		StageCreated:   0,
		StageInterview: 1,
		StageAnalysis:  2,
		StageSynthesis: 3,
		StageReview:    4,
		StageDone:      5,
	}

	curIdx, okCur := order[current]
	tgtIdx, okTgt := order[target]

	if !okCur || !okTgt {
		return false
	}

	return tgtIdx < curIdx
}

func fail(run *Run, store Store, reason error) error {
	run.Stage = StageFailed
	run.Status = RunFailed
	_ = store.Save(run)
	return reason
}
