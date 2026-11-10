package steps

import (
	"context"

	"activity-descriptor/internal/domain"
)

type ReviewStep struct{}

func (r *ReviewStep) Name() domain.Stage { return domain.StageReview }

func (r *ReviewStep) Execute(ctx context.Context, run *domain.Run) error {
	_ = ctx
	_ = run
	return nil
}
