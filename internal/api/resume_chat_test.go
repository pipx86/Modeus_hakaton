package api

import (
	"testing"

	"activity-descriptor/internal/domain"
)

func TestResumeSkipsChatRuns(t *testing.T) {
	m := newMemStore()

	batchRun, _ := m.CreateRun("backend developer")
	chatRun, _ := m.CreateRun("psychologist")

	m.mu.Lock()
	m.runs[chatRun.ID].Type = domain.RunTypeChat
	m.mu.Unlock()

	_ = m.SetStage(batchRun.ID, domain.StageAnalysis)
	_ = m.SetStatus(batchRun.ID, domain.RunRunning)
	_ = m.SetStage(chatRun.ID, domain.StageInterview)
	_ = m.SetStatus(chatRun.ID, domain.RunRunning)

	incomplete, err := m.ListIncompleteRuns()
	if err != nil {
		t.Fatalf("ListIncompleteRuns failed: %v", err)
	}

	if len(incomplete) != 1 {
		t.Fatalf("ожидали ровно 1 неполный прогон (только batch), получили %d", len(incomplete))
	}
	if incomplete[0].ID != batchRun.ID {
		t.Fatalf("возобновился не тот прогон: ожидали %s, получили %s", batchRun.ID, incomplete[0].ID)
	}
}
