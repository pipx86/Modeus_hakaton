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

type Store interface {
	Save(run *Run) error
}

func Orchestrate(
	ctx context.Context,
	run *Run,
	steps []Step,
	store Store,
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

		if !CanTransition(run.Stage, target) {
			return fail(run, store, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, run.Stage, target))
		}

		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			stepCtx, cancel := context.WithTimeout(ctx, stepTO)
			lastErr = st.Execute(stepCtx, run)
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

func fail(run *Run, store Store, reason error) error {
	run.Stage = StageFailed
	run.Status = RunFailed
	_ = store.Save(run)
	return reason
}
