package synthesizer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

func loadConflictInput(t *testing.T) agent.SynthesizerInput {
	t.Helper()
	data, err := os.ReadFile("testdata/conflict_sources.json")
	if err != nil {
		t.Fatalf("read conflict sources: %v", err)
	}
	var in agent.SynthesizerInput
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatalf("parse conflict sources: %v", err)
	}
	return in
}

func nodeIndex(tree []domain.Node) map[string]domain.Node {
	index := make(map[string]domain.Node, len(tree))
	for _, node := range tree {
		index[node.ID] = node
	}
	return index
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestSynthesizeConflictSources(t *testing.T) {
	synth, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), loadConflictInput(t))
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(out.Tree) < 5 {
		t.Fatalf("tree has %d nodes, want at least 5", len(out.Tree))
	}
	if out.Tree[0].Type != domain.NodeRole {
		t.Fatalf("first tree node type = %q, want role", out.Tree[0].Type)
	}
	if err := agent.ValidateOutput(agent.KindSynthesizer, mustMarshal(t, out)); err != nil {
		t.Fatalf("synthesizer output violates contract: %v", err)
	}

	index := nodeIndex(out.Tree)
	for _, node := range out.Tree {
		if err := node.Validate(); err != nil {
			t.Fatalf("node %q without valid evidence: %v", node.ID, err)
		}
		if node.ParentID == nil {
			continue
		}
		parent, ok := index[*node.ParentID]
		if !ok {
			t.Fatalf("node %q references missing parent %q", node.ID, *node.ParentID)
		}
		if !validParentType(node.Type, parent.Type) {
			t.Fatalf("node %q (%s) has invalid parent %q (%s)", node.ID, node.Type, parent.ID, parent.Type)
		}
	}

	const taskID = "tax-task-sql"
	task, ok := index[taskID]
	if !ok {
		t.Fatalf("merged tree misses %q", taskID)
	}
	if task.Title != "Писать SQL-запросы" {
		t.Fatalf("task title = %q, want interview value", task.Title)
	}
	if len(task.Evidence) != 3 {
		t.Fatalf("task evidence = %d, want one per source (3)", len(task.Evidence))
	}
	function, ok := index["tax-fn-reporting"]
	if !ok || function.Title != "Отчётность и визуализация" {
		t.Fatalf("function = %+v, %v; want interview title", function, ok)
	}

	if len(out.Conflicts) != 2 {
		t.Fatalf("conflicts = %d, want 2: %+v", len(out.Conflicts), out.Conflicts)
	}
	for _, conflict := range out.Conflicts {
		if conflict.NodeID != taskID && conflict.NodeID != "tax-fn-reporting" {
			t.Fatalf("unexpected conflict node %q", conflict.NodeID)
		}
		if conflict.Winner.SourceType != domain.SourceInterview {
			t.Fatalf("conflict winner source = %q, want interview", conflict.Winner.SourceType)
		}
		if len(conflict.Values) < 2 {
			t.Fatalf("conflict %q has %d values", conflict.ID, len(conflict.Values))
		}
	}

	if len(out.Messages) != len(out.Conflicts) {
		t.Fatalf("messages = %d, want one per conflict", len(out.Messages))
	}
	for _, message := range out.Messages {
		if message.Kind != "conflict" {
			t.Fatalf("message kind = %q, want conflict", message.Kind)
		}
		var payload domain.Conflict
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			t.Fatalf("conflict message payload is not a conflict: %v", err)
		}
		if payload.ID == "" || payload.Winner.SourceID == "" {
			t.Fatalf("conflict message payload is incomplete: %+v", payload)
		}
	}
}

func TestSynthesizeRejectsNodeWithoutEvidence(t *testing.T) {
	in := agent.SynthesizerInput{
		Profession: "Аналитик данных",
		Interview: []domain.Node{{
			ID: "tax-task-sql", Type: domain.NodeTask, Title: "Писать SQL-запросы",
		}},
	}
	if _, err := Merge(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Merge error = %v, want ErrInvalidInput", err)
	}
}

func TestSynthesizeAddsMissingParents(t *testing.T) {
	in := agent.SynthesizerInput{
		Profession: "Аналитик данных",
		Vacancy: []domain.Node{{
			ID: "tax-task-sql", Type: domain.NodeTask, Title: "Писать SQL-запросы",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "src-vacancy-1", RefID: "line-1", Quote: "писать SQL-запросы"}},
		}},
	}
	out, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	index := nodeIndex(out.Tree)
	task, ok := index["tax-task-sql"]
	if !ok || task.ParentID == nil {
		t.Fatalf("task = %+v, ok=%v; want generated parent", task, ok)
	}
	parent, ok := index[*task.ParentID]
	if !ok || parent.Type != domain.NodeFunction {
		t.Fatalf("task parent = %+v, ok=%v; want function", parent, ok)
	}
	if parent.ParentID == nil {
		t.Fatal("generated function has no role parent")
	}
	if role, ok := index[*parent.ParentID]; !ok || role.Type != domain.NodeRole {
		t.Fatalf("function parent = %+v, ok=%v; want role", role, ok)
	}
	for _, node := range out.Tree {
		if err := node.Validate(); err != nil {
			t.Fatalf("generated node %q has no evidence: %v", node.ID, err)
		}
	}
}

func TestStandardBeatsVacancy(t *testing.T) {
	in := agent.SynthesizerInput{
		Profession: "Аналитик данных",
		Standard: []domain.Node{{
			ID: "n-task", Type: domain.NodeTask, Title: "Название из стандарта",
			Evidence: []domain.Evidence{{ID: "e-s", SourceID: "src-standard", RefID: "req-1", Quote: "стандарт"}},
		}},
		Vacancy: []domain.Node{{
			ID: "n-task", Type: domain.NodeTask, Title: "Название из вакансии",
			Evidence: []domain.Evidence{{ID: "e-v", SourceID: "src-vacancy", RefID: "line-1", Quote: "вакансия"}},
		}},
	}
	out, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	node := nodeIndex(out.Tree)["n-task"]
	if node.Title != "Название из стандарта" {
		t.Fatalf("title = %q, want standard value", node.Title)
	}
	if len(out.Conflicts) != 1 || out.Conflicts[0].Winner.SourceType != domain.SourceStandard {
		t.Fatalf("conflict = %+v, want standard winner", out.Conflicts)
	}
}

func TestSynthesizeRequiresContext(t *testing.T) {
	synth, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := synth.Synthesize(nil, agent.SynthesizerInput{}); err == nil {
		t.Fatal("nil context accepted, want error")
	}
}

func TestNewRejectsUnknownPromptVersion(t *testing.T) {
	if _, err := New(WithPromptVersion("v99")); err == nil {
		t.Fatal("unknown prompt version accepted, want error")
	}
}

func TestMergeNoNodes(t *testing.T) {
	if _, err := Merge(agent.SynthesizerInput{Profession: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Merge empty input error = %v, want ErrInvalidInput", err)
	}
}
