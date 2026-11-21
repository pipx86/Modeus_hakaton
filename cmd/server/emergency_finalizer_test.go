package main

import (
	"context"
	"errors"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
)

type emergencyStore struct {
	nodes  []domain.Node
	stage  domain.Stage
	reason string
}

func (s *emergencyStore) SetStage(_ string, stage domain.Stage) error { s.stage = stage; return nil }
func (s *emergencyStore) ListRunSources(context.Context, string) ([]domain.Source, error) {
	return nil, nil
}
func (s *emergencyStore) CreateInterviewSource(context.Context, string, string, string) error {
	return nil
}
func (s *emergencyStore) SaveTree(_ context.Context, _ string, nodes []domain.Node) error {
	s.nodes = nodes
	return nil
}
func (s *emergencyStore) SaveRunSnapshot(context.Context, string) error { return nil }
func (s *emergencyStore) SaveSynthesisReview(context.Context, string, []domain.Conflict, []agent.AgentMessage) error {
	return nil
}
func (s *emergencyStore) SetDegradedReason(_ string, reason string) error {
	s.reason = reason
	return nil
}

func TestEmergencyFinalizerSavesDeterministicTree(t *testing.T) {
	store := &emergencyStore{}
	primary := func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
		return domain.RunFailed, errors.New("model unavailable")
	}
	finalizer := newEmergencyFinalizer(chat.Finalizer(primary), store)
	session := domain.InterviewSession{
		Profession: "Аналитик данных",
		Questions:  []domain.Question{{ID: "q-1", Text: "Чем вы занимаетесь?"}},
		Answers: []domain.Answer{
			{QuestionID: "q-1", Text: "Я аналитик данных."},
			{QuestionID: "q-1", Text: "Отвечаю за отчётность."},
			{QuestionID: "q-1", Text: "Строю SQL-запросы."},
			{QuestionID: "q-1", Text: "Проверяю качество данных."},
		},
	}
	status, err := finalizer(context.Background(), "run-1", "Аналитик данных", session)
	if err != nil {
		t.Fatalf("finalizer: %v", err)
	}
	if status != domain.RunDegraded || store.stage != domain.StageDone {
		t.Fatalf("status=%s stage=%s", status, store.stage)
	}
	if len(store.nodes) != 22 {
		t.Fatalf("nodes = %d", len(store.nodes))
	}
	if store.reason == "" {
		t.Fatal("degraded reason was not saved")
	}
}
