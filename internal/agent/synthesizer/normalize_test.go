package synthesizer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// fakeChatClient is a scripted llm.ChatClient: it records every call and returns
// one response or error per call.
type fakeChatClient struct {
	mu        sync.Mutex
	responses []string
	errs      []error
	inputs    [][]llm.Message
}

func (f *fakeChatClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	f.mu.Lock()
	index := len(f.inputs)
	f.inputs = append(f.inputs, append([]llm.Message(nil), messages...))
	f.mu.Unlock()
	if index < len(f.errs) && f.errs[index] != nil {
		return "", f.errs[index]
	}
	if index >= len(f.responses) {
		return "", errors.New("unexpected llm call")
	}
	return f.responses[index], nil
}

func (f *fakeChatClient) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

func (f *fakeChatClient) lastInput() []llm.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) == 0 {
		return nil
	}
	return f.inputs[len(f.inputs)-1]
}

func (f *fakeChatClient) promptText() string {
	messages := f.lastInput()
	var text strings.Builder
	for _, message := range messages {
		text.WriteString(message.Content)
		text.WriteString("\n")
	}
	return text.String()
}

// assertProtectedNodeFields verifies that normalization preserved everything
// the deterministic merge owns: tree size and order, ids, types, parents,
// evidence and ZUN (the only allowed changes are title/description).
func assertProtectedNodeFields(t *testing.T, want, got agent.SynthesizerOutput) {
	t.Helper()
	if len(got.Tree) != len(want.Tree) {
		t.Fatalf("tree size = %d, want %d", len(got.Tree), len(want.Tree))
	}
	for i := range want.Tree {
		w, g := want.Tree[i], got.Tree[i]
		if w.ID != g.ID || w.Type != g.Type || !sameParent(w.ParentID, g.ParentID) {
			t.Fatalf("node %d identity changed: want %+v, got %+v", i, w, g)
		}
		if !reflect.DeepEqual(w.Evidence, g.Evidence) {
			t.Fatalf("node %q evidence changed: want %+v, got %+v", w.ID, w.Evidence, g.Evidence)
		}
		if !reflect.DeepEqual(w.Zun, g.Zun) {
			t.Fatalf("node %q ZUN changed", w.ID)
		}
		if strings.TrimSpace(g.Title) == "" {
			t.Fatalf("node %q lost its title", g.ID)
		}
	}
	if !reflect.DeepEqual(want.Conflicts, got.Conflicts) {
		t.Fatalf("conflicts changed: want %+v, got %+v", want.Conflicts, got.Conflicts)
	}
	if !reflect.DeepEqual(want.Messages, got.Messages) {
		t.Fatalf("messages changed: want %+v, got %+v", want.Messages, got.Messages)
	}
}

func TestSynthesizeNormalizesTitlesWithLLM(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{`{"nodes":[
		{"id":"tax-task-sql","title":"Писать SQL-запросы и выгрузки","type":"role","parent_id":"hacked-parent","evidence":[{"id":"fake","source_id":"src-evil","ref_id":"x","quote":"fake"}]},
		{"id":"tax-task-dashboards","title":"Строить дашборды в BI"},
		{"id":"unknown-node","title":"Выдуманный узел"}
	]}`}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if calls := client.calls(); calls != 1 {
		t.Fatalf("llm calls = %d, want exactly 1", calls)
	}
	assertProtectedNodeFields(t, deterministic, out)

	index := nodeIndex(out.Tree)
	if got := index["tax-task-sql"].Title; got != "Писать SQL-запросы и выгрузки" {
		t.Fatalf("normalized title = %q", got)
	}
	// Fields outside the normalization contract that the model tried to send
	// (type/parent_id/evidence) are ignored.
	task := index["tax-task-sql"]
	if task.Type != domain.NodeTask {
		t.Fatalf("node type = %q, the model must not change it", task.Type)
	}
	if task.ParentID == nil || *task.ParentID != "tax-fn-reporting" {
		t.Fatalf("node parent = %v, the model must not change it", task.ParentID)
	}
	for _, evidence := range task.Evidence {
		if evidence.ID == "fake" || evidence.SourceID == "src-evil" {
			t.Fatalf("model-provided evidence leaked into the tree: %+v", evidence)
		}
	}
	// Normalization must return a new value and never mutate the deterministic
	// merge result in place.
	if got := nodeIndex(deterministic.Tree)["tax-task-sql"].Title; got != "Писать SQL-запросы" {
		t.Fatalf("deterministic tree was mutated by normalization: title = %q", got)
	}
	if got := index["tax-task-dashboards"].Title; got != "Строить дашборды в BI" {
		t.Fatalf("normalized title = %q", got)
	}
	if _, ok := index["unknown-node"]; ok {
		t.Fatal("unknown node id from the model was added to the tree")
	}
	// The model may not rewrite conflict winners.
	for _, conflict := range out.Conflicts {
		var found bool
		for _, original := range deterministic.Conflicts {
			if reflect.DeepEqual(conflict, original) {
				found = true
			}
		}
		if !found {
			t.Fatalf("conflict %+v was modified by normalization", conflict)
		}
	}

	prompt := client.promptText()
	if !strings.Contains(prompt, in.Profession) {
		t.Fatalf("prompt does not contain the profession %q", in.Profession)
	}
	if !strings.Contains(prompt, llm.UntrustedOpenMarker) || !strings.Contains(prompt, llm.UntrustedCloseMarker) {
		t.Fatal("prompt does not delimiter the merged node payload as untrusted data")
	}
	if !strings.Contains(prompt, "tax-task-sql") {
		t.Fatal("prompt does not contain the merged node ids")
	}
	if len(client.lastInput()) < 2 || !strings.Contains(client.lastInput()[0].Content, normalizationRules[:40]) {
		t.Fatal("prompt misses the normalization system rules")
	}
}

func TestSynthesizeNormalizationUpdatesOnlyExistingDescriptions(t *testing.T) {
	rootID := "role-1"
	in := agent.SynthesizerInput{
		Profession: "Аналитик данных",
		Interview: []domain.Node{
			{
				ID: rootID, Type: domain.NodeRole, Title: "Аналитик",
				Description: "исходное описание",
				Evidence:    []domain.Evidence{{ID: "ev-1", SourceID: "src-interview", RefID: "q1", Quote: "Я аналитик данных."}},
			},
			{
				ID: "fn-1", Type: domain.NodeFunction, Title: "Отчётность", ParentID: &rootID,
				Evidence: []domain.Evidence{{ID: "ev-2", SourceID: "src-interview", RefID: "q2", Quote: "Готовлю отчёты."}},
			},
		},
	}
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{`{"nodes":[
		{"id":"role-1","title":"Аналитик данных","description":"Анализ данных и отчётность"},
		{"id":"fn-1","title":"Отчётность и визуализация","description":"Выдуманное описание"}
	]}`}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if calls := client.calls(); calls != 1 {
		t.Fatalf("llm calls = %d, want exactly 1", calls)
	}
	assertProtectedNodeFields(t, deterministic, out)

	index := nodeIndex(out.Tree)
	role := index["role-1"]
	if role.Title != "Аналитик данных" || role.Description != "Анализ данных и отчётность" {
		t.Fatalf("role = %+v, want normalized title and existing description", role)
	}
	fn := index["fn-1"]
	if fn.Title != "Отчётность и визуализация" {
		t.Fatalf("function title = %q, want normalized", fn.Title)
	}
	if fn.Description != "" {
		t.Fatalf("function description = %q, the model must not invent a new description", fn.Description)
	}
}

func TestSynthesizeNormalizationIgnoresEmptyTitles(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{`{"nodes":[{"id":"tax-task-sql","title":"   ","description":""}]}`}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !reflect.DeepEqual(out, deterministic) {
		t.Fatalf("empty title changed the deterministic output: %+v", nodeIndex(out.Tree)["tax-task-sql"])
	}
}

func TestSynthesizeNormalizationSanitizesAppliedText(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{`{"nodes":[{"id":"tax-task-sql","title":"  Писать\n\tSQL-запросы  "}]}`}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	assertProtectedNodeFields(t, deterministic, out)
	if got, want := nodeIndex(out.Tree)["tax-task-sql"].Title, "Писать SQL-запросы"; got != want {
		t.Fatalf("normalized title = %q, want whitespace-normalized %q", got, want)
	}
}

func TestSynthesizeNormalizationFallsBackOnInvalidJSON(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{"Конечно! Вот результат: not a json object"}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize must not fail on invalid normalization JSON: %v", err)
	}
	if calls := client.calls(); calls != 1 {
		t.Fatalf("llm calls = %d, want exactly 1", calls)
	}
	if !reflect.DeepEqual(out, deterministic) {
		t.Fatal("invalid normalization answer must keep the deterministic tree")
	}
}

func TestSynthesizeNormalizationFallsBackOnClientError(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{errs: []error{errors.New("provider unavailable")}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize must not fail when the model call fails: %v", err)
	}
	if calls := client.calls(); calls != 1 {
		t.Fatalf("llm calls = %d, want exactly 1", calls)
	}
	if !reflect.DeepEqual(out, deterministic) {
		t.Fatal("failed normalization must keep the deterministic tree")
	}
}

func TestSynthesizeNormalizationIgnoresUnknownIDs(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{`{"nodes":[{"id":"invented-1","title":"Чужой узел"},{"id":"","title":"Без id"}]}`}}
	synth, err := New(WithClient(client))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !reflect.DeepEqual(out, deterministic) {
		t.Fatal("unknown ids must be ignored and the deterministic tree preserved")
	}
}

func TestSynthesizeWithoutClientDoesNotCallModel(t *testing.T) {
	in := loadConflictInput(t)
	deterministic, err := Merge(in)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	client := &fakeChatClient{responses: []string{`{"nodes":[]}`}}
	synth, err := New(WithClient(nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := synth.Synthesize(context.Background(), in)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if calls := client.calls(); calls != 0 {
		t.Fatalf("llm calls = %d, want 0 for a deterministic synthesizer", calls)
	}
	if !reflect.DeepEqual(out, deterministic) {
		t.Fatal("nil client must produce exactly the deterministic output")
	}
}

func TestNewWithClientRejectsUnknownPromptVersion(t *testing.T) {
	if _, err := New(WithClient(&fakeChatClient{}), WithPromptVersion("v99")); err == nil {
		t.Fatal("unknown prompt version accepted, want error")
	}
}
