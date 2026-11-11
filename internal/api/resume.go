package api

import (
	"context"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging" // === NEW: Импорт логгера ===
	"activity-descriptor/internal/steps"
)

type IncompleteLister interface {
	ListIncompleteRuns() ([]*domain.Run, error)
}

type TreeSaver interface {
	SaveTree(ctx context.Context, id string, nodes []domain.Node) error
}

type ResumeProcessor struct {
	store      IncompleteLister
	treeSaver  TreeSaver
	reader     domain.VacancyReader
	maxRetries int
	timeout    time.Duration
}

func NewResumeProcessor(lister IncompleteLister, saver TreeSaver, reader domain.VacancyReader) *ResumeProcessor {
	return &ResumeProcessor{
		store:      lister,
		treeSaver:  saver,
		reader:     reader,
		maxRetries: 2,
		timeout:    30 * time.Second,
	}
}

func (rp *ResumeProcessor) ProcessIncompleteRuns(ctx context.Context) error {
	incomplete, err := rp.store.ListIncompleteRuns()
	if err != nil {
		logging.Logger.Error("resume_list_incomplete_failed", "error", err)
		return err
	}

	if len(incomplete) == 0 {
		logging.Logger.Info("resume_no_incomplete_runs")
		return nil
	}

	logging.Logger.Info("resume_found_incomplete_runs", "count", len(incomplete))

	realSteps := []domain.Step{
		&steps.InterviewStep{},
		&steps.AnalysisStep{},
		&steps.SynthesisStep{},
		&steps.ReviewStep{TopK: 10, Store: rp.treeSaver},
	}

	for _, run := range incomplete {
		select {
		case <-ctx.Done():
			logging.Logger.Warn("resume_context_cancelled", "run_id", run.ID, "stage", run.Stage)
			return ctx.Err()
		default:
		}

		logging.Logger.Info("resume_continuing_run", "run_id", run.ID, "stage", run.Stage)

		ds, ok := rp.store.(domain.Store)
		if !ok {
			logging.Logger.Error("resume_store_not_domain_interface", "run_id", run.ID)
			continue
		}

		// Промежуточные данные живут в памяти, поэтому пересчитываем этапы сначала.
		run.Stage = domain.StageCreated
		err := domain.Orchestrate(ctx, run, realSteps, ds, rp.reader, rp.maxRetries, rp.timeout)
		if err != nil {
			logging.Logger.Error("resume_orchestration_failed", "run_id", run.ID, "error", err)
			continue
		}

		logging.Logger.Info("resume_run_completed_successfully", "run_id", run.ID)
	}

	return nil
}
