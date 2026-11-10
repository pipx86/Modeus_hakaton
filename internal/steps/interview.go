package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/domain"
)

type InterviewStep struct{}

func (i *InterviewStep) Name() domain.Stage { return domain.StageInterview }

func (i *InterviewStep) Execute(_ context.Context, _ *domain.Run) error {
	return fmt.Errorf("interview step requires data; use ExecuteWithData")
}

func (i *InterviewStep) ExecuteWithData(ctx context.Context, run *domain.Run, vacancies []domain.VacancyRecord) error {
	_ = ctx
	_ = run
	_ = vacancies
	return nil
}
