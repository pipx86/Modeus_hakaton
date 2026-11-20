package main

import (
	"context"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/learning"
)

type acceptanceLearningStore struct {
	run   domain.Run
	plan  learning.Plan
	stage domain.Stage
}

func (s *acceptanceLearningStore) GetRun(string) (*domain.Run, error) { return &s.run, nil }

func (s *acceptanceLearningStore) SetStage(_ string, stage domain.Stage) error {
	s.stage = stage
	return nil
}

func (s *acceptanceLearningStore) SaveLearningPlan(_ string, plan learning.Plan) error {
	s.plan = plan
	return nil
}

type acceptancePlanner struct {
	transcript string
	plan       learning.Plan
}

func (p *acceptancePlanner) PlanFromTranscript(_ context.Context, _, transcript string) (learning.Plan, error) {
	p.transcript = transcript
	return p.plan, nil
}

// TestLearningAcceptanceFlow covers the acceptance path: an interview is
// finished, the planner receives the user's answers, and the plan is saved as
// the result of the same learning run.
func TestLearningAcceptanceFlow(t *testing.T) {
	store := &acceptanceLearningStore{run: domain.Run{ID: "run-learning", Type: domain.RunTypeLearning, Profession: "DevOps-инженер"}}
	planner := &acceptancePlanner{plan: learning.Plan{
		Profession: "DevOps-инженер",
		Summary:    "Путь в DevOps",
		Weeks:      12,
		Stages: []learning.Stage{{
			Title:  "Kubernetes",
			Goal:   "Перевести pet-проект в кластер",
			Topics: []string{"pods", "services"},
		}},
	}}
	finalizer := newHybridFinalizer(store, nil, planner)
	session := domain.InterviewSession{
		Questions: []domain.Question{{ID: "q-1", Text: "Что вы уже умеете?"}},
		Answers:   []domain.Answer{{QuestionID: "q-1", Text: "Знаю Docker, Git и Linux, Kubernetes пока не изучал."}},
	}
	status, err := finalizer(context.Background(), "run-learning", "DevOps-инженер", session)
	if err != nil {
		t.Fatalf("acceptance finalizer: %v", err)
	}
	if status != domain.RunDone || store.stage != domain.StageDone {
		t.Fatalf("status=%s stage=%s, want done", status, store.stage)
	}
	if !strings.Contains(planner.transcript, "Docker") {
		t.Fatalf("planner did not receive interview answers: %q", planner.transcript)
	}
	if store.plan.Profession != "DevOps-инженер" || len(store.plan.Stages) != 1 {
		t.Fatalf("plan was not saved: %+v", store.plan)
	}
}

func TestInterviewAcceptanceRoutesToTreeFinalizer(t *testing.T) {
	store := &acceptanceLearningStore{run: domain.Run{ID: "run-interview", Type: domain.RunTypeChat, Profession: "Аналитик"}}
	called := false
	treeFinalizer := func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
		called = true
		return domain.RunDone, nil
	}
	finalizer := newHybridFinalizer(store, treeFinalizer, nil)
	status, err := finalizer(context.Background(), "run-interview", "Аналитик", domain.InterviewSession{})
	if err != nil {
		t.Fatalf("tree acceptance: %v", err)
	}
	if !called || status != domain.RunDone {
		t.Fatalf("tree finalizer called=%v status=%s", called, status)
	}
	if store.plan.Profession != "" {
		t.Fatal("interview run must not save a learning plan")
	}
}
