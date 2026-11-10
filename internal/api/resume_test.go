package api

import (
	"context"
	"testing"

	"activity-descriptor/internal/domain"
)

func TestResumeProcessesIncompleteRun(t *testing.T) {
	ms := newMemStore()
	run, _ := ms.CreateRun("backend developer")

	// Симулируем прерванный на середине прогон.
	_ = ms.SetStatus(run.ID, domain.RunRunning)
	_ = ms.SetStage(run.ID, domain.StageAnalysis)

	rp := NewResumeProcessor(ms, nil, mockVacancyReader{})
	err := rp.ProcessIncompleteRuns(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := ms.GetRun(run.ID)
	if got.Status != domain.RunDone || got.Stage != domain.StageDone {
		t.Fatalf("run должен был дойти до done: status=%s stage=%s", got.Status, got.Stage)
	}
}

func TestResumeSkipsCompletedRuns(t *testing.T) {
	ms := newMemStore()
	run, _ := ms.CreateRun("data analyst")
	_ = ms.SetStatus(run.ID, domain.RunDone)
	_ = ms.SetStage(run.ID, domain.StageDone)

	rp := NewResumeProcessor(ms, nil, mockVacancyReader{})
	_ = rp.ProcessIncompleteRuns(context.Background())

	listed, _ := ms.ListIncompleteRuns()
	if len(listed) != 0 {
		t.Fatalf("завершённый прогон не должен попадать в список incomplete")
	}
}

func TestResumeHandlesEmptyList(t *testing.T) {
	ms := newMemStore()
	rp := NewResumeProcessor(ms, nil, mockVacancyReader{})
	err := rp.ProcessIncompleteRuns(context.Background())
	if err != nil {
		t.Fatalf("пустой список не должен давать ошибку: %v", err)
	}
}

type mockVacancyReader struct{}

func (mockVacancyReader) ByProfession(_ context.Context, _ string, _ int) ([]domain.VacancyRecord, error) {
	return []domain.VacancyRecord{
		{ID: "v1", Title: "Go Developer", Description: "go docker kubernetes python sql", SourceURL: ""},
	}, nil
}

var _ domain.VacancyReader = mockVacancyReader{}
