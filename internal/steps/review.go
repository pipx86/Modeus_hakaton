package steps

import (
	"context"
	"fmt"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/domain"
)

type TreeStorer interface {
	SaveTree(ctx context.Context, id string, nodes []domain.Node) error
}

type ReviewStep struct {
	Profiles []analysis.CandidateProfile
	TopK     int
	Store    TreeStorer
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

	levelData, _ := run.Data["level_data"].([]map[string]any)
	nodes, err := analysis.BuildActivityTree(run.Profession, levelData, reports)
	if err != nil {
		return fmt.Errorf("build activity tree: %w", err)
	}

	if r.Store != nil && len(nodes) > 0 {
		if err := r.Store.SaveTree(ctx, run.ID, nodes); err != nil {
			return fmt.Errorf("save tree: %w", err)
		}
	}

	return nil
}
