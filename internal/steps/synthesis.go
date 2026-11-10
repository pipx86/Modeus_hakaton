package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/domain"
)

type SynthesisStep struct{}

func (s *SynthesisStep) Name() domain.Stage { return domain.StageSynthesis }

func (s *SynthesisStep) Execute(_ context.Context, _ *domain.Run) error {
	return fmt.Errorf("synthesis step requires accumulated state; use ExecuteWithData")
}

func (s *SynthesisStep) ExecuteWithData(ctx context.Context, run *domain.Run, vacancies []domain.VacancyRecord) error {
	_ = ctx
	_ = run
	_ = vacancies
	return nil
}
