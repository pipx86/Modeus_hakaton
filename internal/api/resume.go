package api

import (
	"context"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging" // === NEW: Импорт логгера ===
	"activity-descriptor/internal/sse"
	"activity-descriptor/internal/steps"
)

type IncompleteLister interface {
	ListIncompleteRuns() ([]*domain.Run, error)
}

type TreeSaver interface {
	SaveTree(ctx context.Context, id string, nodes []domain.Node) error
}

type ResumeProcessor struct {
	store     IncompleteLister
	treeSaver TreeSaver
	reader    domain.VacancyReader
	events    sse.EventStore
	plan      steps.BatchPlan
}

// BindResumeProcessor подключает события восстанавливаемых исследований к API.
func (s *Server) BindResumeProcessor(rp *ResumeProcessor) { rp.events = s.eventStore }

func NewResumeProcessor(lister IncompleteLister, saver TreeSaver, reader domain.VacancyReader) *ResumeProcessor {
	return &ResumeProcessor{
		store:     lister,
		treeSaver: saver,
		reader:    reader,
	}
}

// SetBatchPlan wires the same batch plan the create-run handler uses. The two
// paths therefore run the same step chain with the same retries and timeouts.
func (rp *ResumeProcessor) SetBatchPlan(plan steps.BatchPlan) { rp.plan = plan }

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

	plan := rp.plan
	if len(plan.AI) == 0 && len(plan.Fallback) == 0 {
		treeStore, _ := rp.treeSaver.(steps.AITreeStore)
		plan = steps.NewBatchPlan(steps.BatchOptions{Store: treeStore, VacancyTopK: 10})
	}
	// The transcript of an AI batch run is persisted as an interview source
	// before the first orchestration attempt, so a restart can restore the same
	// run.Data the handler path had. Without the capability (or without the
	// source) the vacancy fallback below runs exactly as before.
	transcriptStore, _ := rp.treeSaver.(steps.TranscriptStore)

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

		hasTranscript := false
		if transcriptStore != nil {
			transcript, sourceID, found, loadErr := steps.LoadInterviewTranscript(ctx, transcriptStore, run.ID)
			if loadErr != nil {
				logging.Logger.Error("resume_transcript_load_failed", "run_id", run.ID, "error", loadErr)
			} else if found {
				steps.SetTranscript(run, transcript, sourceID)
				hasTranscript = true
			}
		}
		runSteps, stepTimeout := plan.For(hasTranscript)
		pipeline := "vacancy"
		if plan.HasAI() && hasTranscript {
			pipeline = "ai"
		}

		// Промежуточные данные живут в памяти, поэтому пересчитываем этапы сначала.
		run.Stage = domain.StageCreated
		var state domain.Store = ds
		if rp.events != nil {
			state = eventRunStore{ds, rp.events}
		}
		err := domain.Orchestrate(ctx, run, runSteps, state, rp.reader, plan.MaxRetries, stepTimeout)
		if err != nil {
			logging.Logger.Error("resume_orchestration_failed", "run_id", run.ID, "pipeline", pipeline, "error", err)
			continue
		}

		logging.Logger.Info("resume_run_completed_successfully", "run_id", run.ID, "pipeline", pipeline)
	}

	return nil
}
