package steps

import (
	"context"
	"errors"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type fakeAnalystClient struct{}

func (fakeAnalystClient) Chat(context.Context, []llm.Message) (string, error) {
	return `{"entities":[{"id":"role-1","type":"role","title":"Аналитик данных","evidence":[{"id":"e1","source_id":"src-interview","ref_id":"q-1","quote":"готовлю отчёты"}]}]}`, nil
}

type fakeTreeStore struct {
	nodes []domain.Node
	err   error
}

func (s *fakeTreeStore) SaveTree(_ context.Context, _ string, nodes []domain.Node) error {
	s.nodes = nodes
	return s.err
}

func TestAgentAnalysisAndSynthesisSteps(t *testing.T) {
	run := &domain.Run{
		ID:         "run-1",
		Profession: "Аналитик данных",
		Data:       map[string]any{"transcript": "Я аналитик данных и готовлю отчёты."},
	}
	analysis := &AgentAnalysisStep{Client: fakeAnalystClient{}}
	if err := analysis.ExecuteWithData(context.Background(), run, nil); err != nil {
		t.Fatalf("analysis: %v", err)
	}
	nodes, ok := run.Data["interview_nodes"].([]domain.Node)
	if !ok || len(nodes) == 0 {
		t.Fatalf("interview_nodes = %#v", run.Data["interview_nodes"])
	}

	synthesis := &AgentSynthesisStep{}
	if err := synthesis.Execute(context.Background(), run); err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	tree, ok := run.Data["tree"].([]domain.Node)
	if !ok || len(tree) == 0 {
		t.Fatalf("tree = %#v", run.Data["tree"])
	}

	store := &fakeTreeStore{}
	save := &SaveAITreeStep{Store: store}
	if err := save.Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(store.nodes) != len(tree) {
		t.Fatalf("saved nodes = %d, want %d", len(store.nodes), len(tree))
	}
}

func TestAgentAnalysisStepRequiresTranscript(t *testing.T) {
	run := &domain.Run{ID: "run-2", Profession: "Аналитик"}
	step := &AgentAnalysisStep{Client: fakeAnalystClient{}}
	if err := step.ExecuteWithData(context.Background(), run, nil); err == nil {
		t.Fatal("expected error for empty transcript")
	}
}

func TestSaveAITreeStepPropagatesStoreError(t *testing.T) {
	run := &domain.Run{ID: "run-3", Data: map[string]any{
		"tree": []domain.Node{{ID: "n1", Type: domain.NodeRole, Title: "Роль",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "s1", Quote: "q"}}}},
	}}
	store := &fakeTreeStore{err: errors.New("boom")}
	step := &SaveAITreeStep{Store: store}
	if err := step.Execute(context.Background(), run); err == nil {
		t.Fatal("expected store error")
	}
}
