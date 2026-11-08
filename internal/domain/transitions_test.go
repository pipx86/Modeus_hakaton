package domain

import "testing"

func TestStageTransitionsAllowed(t *testing.T) {
	good := []struct{ from, to Stage }{
		{StageCreated, StageInterview},
		{StageInterview, StageAnalysis},
		{StageAnalysis, StageSynthesis},
		{StageSynthesis, StageReview},
		{StageReview, StageDone},
		{StageCreated, StageFailed},
		{StageInterview, StageFailed},
		{StageReview, StageFailed},
	}
	for _, g := range good {
		if !CanTransition(g.from, g.to) {
			t.Fatalf("переход %s -> %s должен быть разрешён", g.from, g.to)
		}
	}
}

func TestStageTransitionsBlocked(t *testing.T) {
	bad := []struct{ from, to Stage }{
		{StageCreated, StageDone},
		{StageInterview, StageReview},
		{StageDone, StageInterview},
		{StageFailed, StageCreated},
		{StageDone, StageFailed},
	}
	for _, b := range bad {
		if CanTransition(b.from, b.to) {
			t.Fatalf("переход %s -> %s должен быть запрещён", b.from, b.to)
		}
	}
}
