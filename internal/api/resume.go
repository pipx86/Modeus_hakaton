package api

import (
	"context"
	"log"
	"time"

	"activity-descriptor/internal/domain"
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
		return err
	}

	if len(incomplete) == 0 {
		log.Println("[resume] no incomplete runs found")
		return nil
	}

	log.Printf("[resume] found %d incomplete run(s), resuming...", len(incomplete))

	realSteps := []domain.Step{
		&steps.InterviewStep{},
		&steps.AnalysisStep{},
		&steps.SynthesisStep{},
		&steps.ReviewStep{TopK: 10, Store: rp.treeSaver},
	}

	for _, run := range incomplete {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("[resume] continuing run %s from stage=%s", run.ID, run.Stage)

		ds, ok := rp.store.(domain.Store)
		if !ok {
			log.Printf("[resume] store does not implement domain.Store; skipping run %s", run.ID)
			continue
		}

		err := domain.Orchestrate(ctx, run, realSteps, ds, rp.reader, rp.maxRetries, rp.timeout)
		if err != nil {
			log.Printf("[resume] run %s failed during resume: %v", run.ID, err)
			continue
		}

		log.Printf("[resume] run %s completed successfully", run.ID)
	}

	return nil
}
