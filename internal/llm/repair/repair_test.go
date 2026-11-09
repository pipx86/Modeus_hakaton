package repair

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/validate"
)

type fakeClient struct {
	answers  []string
	err      error
	calls    int
	messages [][]llm.Message
}

func (f *fakeClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	f.calls++
	f.messages = append(f.messages, append([]llm.Message(nil), messages...))
	if f.err != nil {
		return "", f.err
	}
	if len(f.answers) == 0 {
		return "", errors.New("no answers configured")
	}
	index := f.calls - 1
	if index >= len(f.answers) {
		index = len(f.answers) - 1
	}
	return f.answers[index], nil
}

func messages() []llm.Message {
	return []llm.Message{{Role: llm.RoleUser, Content: "собери узел"}}
}

func validNodeJSON() string {
	return `{
		"id": "n-1",
		"type": "action",
		"title": "Пишет SQL-запросы",
		"evidence": [{"id": "e-1", "source_id": "s-1", "quote": "пишу SQL"}]
	}`
}

func nodeSchema(t *testing.T) []byte {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join("..", "schema", "node.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return schema
}

func nodeValidator(t *testing.T) Validator {
	t.Helper()
	schema := nodeSchema(t)
	return func(answer string) error {
		return validate.Schema([]byte(answer), schema)
	}
}

func TestValidFirstTryDoesNotRepair(t *testing.T) {
	fake := &fakeClient{answers: []string{validNodeJSON()}}
	client, err := New(fake, nodeValidator(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	answer, err := client.Chat(context.Background(), messages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validNodeJSON() {
		t.Fatalf("answer changed: %q", answer)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1", fake.calls)
	}
}

func TestRepairsThenValid(t *testing.T) {
	fake := &fakeClient{answers: []string{
		"not-json",
		`{"id": "n-1"}`,
		validNodeJSON(),
	}}
	client, err := New(fake, nodeValidator(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	answer, err := client.Chat(context.Background(), messages())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != validNodeJSON() {
		t.Fatalf("answer = %q", answer)
	}
	if fake.calls != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 repairs)", fake.calls)
	}
	last := fake.messages[len(fake.messages)-1]
	if len(last) != 5 {
		t.Fatalf("repair history length = %d, want 5 (initial + 2 repair pairs)", len(last))
	}
	if last[3].Role != llm.RoleAssistant || last[4].Role != llm.RoleUser {
		t.Fatalf("repair roles = %#v", last)
	}
}

func TestGivesUpAfterTwoRepairs(t *testing.T) {
	fake := &fakeClient{answers: []string{"broken"}}
	client, err := New(fake, nodeValidator(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	run := &domain.Run{Status: domain.RunRunning}
	_, err = client.Chat(context.Background(), messages())
	MarkRunFailed(run, err)
	if err == nil {
		t.Fatal("expected terminal error")
	}
	if run.Status != domain.RunFailed {
		t.Fatalf("run status = %q, want %q", run.Status, domain.RunFailed)
	}
	var terminal *Error
	if !errors.As(err, &terminal) {
		t.Fatalf("err = %T, want *Error", err)
	}
	if terminal.Status != domain.RunFailed {
		t.Fatalf("status = %q, want %q", terminal.Status, domain.RunFailed)
	}
	if terminal.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", terminal.Attempts)
	}
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err = %v, want ErrInvalidResponse", err)
	}
	if fake.calls != 3 {
		t.Fatalf("calls = %d, want 3", fake.calls)
	}
}

func TestTenBrokenResponsesDoNotPanic(t *testing.T) {
	broken := []string{
		"",
		"not-json",
		`{"id": "n-1"}`,
		`{"id": "n-1", "type": "action"}`,
		`{"id": "n-1", "type": "action", "title": "" , "evidence": []}`,
		`{"id": "n-1", "type": "wrong", "title": "x", "evidence": [{"id":"e","source_id":"s","quote":"q"}]}`,
		`{"id": "n-1", "type": "action", "title": "x", "evidence": []}`,
		`{"id": "n-1", "type": "action", "title": "x", "evidence": [{}]}`,
		"```json\n{}\n```",
		`{"id":"n-1","type":"action","title":"x","evidence":"not-array"}`,
	}

	for index, answer := range broken {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			fake := &fakeClient{answers: []string{answer}}
			client, err := New(fake, nodeValidator(t))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			_, err = client.Chat(context.Background(), messages())
			if err == nil {
				t.Fatalf("broken answer %d accepted", index)
			}
			var terminal *Error
			if !errors.As(err, &terminal) {
				t.Fatalf("err = %T, want *Error", err)
			}
			if terminal.Status != domain.RunFailed {
				t.Fatalf("status = %q", terminal.Status)
			}
			if fake.calls > 3 {
				t.Fatalf("calls = %d, want <= 3", fake.calls)
			}
		})
	}
}

func TestTransportErrorIsExplicit(t *testing.T) {
	sentinel := errors.New("upstream 500")
	fake := &fakeClient{err: sentinel}
	client, err := New(fake, nodeValidator(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), messages())
	var terminal *Error
	if !errors.As(err, &terminal) {
		t.Fatalf("err = %T, want *Error", err)
	}
	if terminal.Status != domain.RunFailed {
		t.Fatalf("status = %q", terminal.Status)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := New(nil, nodeValidator(t)); err == nil {
		t.Fatal("expected error for nil client")
	}
	if _, err := New(&fakeClient{}, nil); err == nil {
		t.Fatal("expected error for nil validator")
	}
}
