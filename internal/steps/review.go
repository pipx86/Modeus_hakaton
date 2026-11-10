package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
)

type ReviewStep struct {
	Profiles []analysis.CandidateProfile
	TopK     int
}

func (r *ReviewStep) Name() domain.Stage { return domain.StageReview }

func (r *ReviewStep) Execute(ctx context.Context, run *domain.Run) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if run.Data == nil {
		return fmt.Errorf("run.Data not initialized; Analysis/Synthesis steps must precede Review")
	}

	skillFreq, ok := run.Data["skill_freq"].(map[string]int)
	if !ok || len(skillFreq) == 0 {
		return fmt.Errorf("skill_freq absent or empty in run.Data")
	}
	weightsRaw, _ := run.Data["hybrid_weights_raw"].(map[string]float64)

	market := analysis.MarketData{
		SkillFreq:     skillFreq,
		HybridWeights: weightsRaw,
	}

	profiles := r.Profiles
	if len(profiles) == 0 {
		profiles = []analysis.CandidateProfile{
			{Name: "default-student", Skills: nil},
		}
	}

	topK := r.TopK
	if topK <= 0 {
		topK = 10
	}

	reports := analysis.ComputeGap(profiles, market, topK)

	run.Data["gap_reports"] = reports

	return nil
}
