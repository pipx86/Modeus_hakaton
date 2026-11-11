package interviewer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type dialogResponse struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

type dialogExpect struct {
	Levels         []string `json:"levels"`
	Questions      int      `json:"questions"`
	Clarifications int      `json:"clarifications"`
	Fallbacks      int      `json:"fallbacks"`
	ModelErrors    int      `json:"model_errors"`
}

type dialogFile struct {
	Name       string            `json:"name"`
	SessionID  string            `json:"session_id"`
	Respondent domain.Respondent `json:"respondent"`
	Responses  []dialogResponse  `json:"responses"`
	Answers    []string          `json:"answers"`
	Expect     dialogExpect      `json:"expect"`
}

// scriptedClient replays a fixed list of model answers and never performs I/O.
type scriptedClient struct {
	t         *testing.T
	responses []dialogResponse
	calls     int
	failures  int
}

func (c *scriptedClient) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if ctx == nil {
		c.t.Error("chat called with nil context")
	}
	if len(messages) < 2 {
		c.t.Fatalf("want system+user messages, got %d", len(messages))
	}
	if messages[0].Role != llm.RoleSystem || !strings.Contains(messages[0].Content, "Интервьюер") {
		c.t.Fatalf("system prompt is not the interviewer prompt")
	}
	if strings.TrimSpace(messages[len(messages)-1].Content) == "" {
		c.t.Error("user prompt is empty")
	}
	c.calls++
	if c.calls > len(c.responses) {
		c.failures++
		return "", errors.New("script exhausted")
	}
	response := c.responses[c.calls-1]
	switch response.Kind {
	case "error":
		c.failures++
		return "", errors.New(response.Error)
	case "invalid":
		return response.Text, nil
	default:
		return response.Text, nil
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestInterviewerDialogs(t *testing.T) {
	files, err := filepath.Glob("testdata/dialog_*.json")
	if err != nil {
		t.Fatalf("glob dialogs: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("want 5 dialog fixtures, got %d: %v", len(files), files)
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read dialog: %v", err)
			}
			var dialog dialogFile
			if err := json.Unmarshal(data, &dialog); err != nil {
				t.Fatalf("parse dialog: %v", err)
			}
			client := &scriptedClient{t: t, responses: dialog.Responses}
			ag, err := New(client, WithMaxRepairs(0))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			session, err := NewSession(ag, dialog.SessionID, dialog.Respondent)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}

			var levels []string
			clarifications, fallbacks := 0, 0
			for !session.Done() {
				question, err := session.Next(context.Background())
				if err != nil {
					t.Fatalf("Next must not fail on model errors: %v", err)
				}
				if question.ID == "" || strings.TrimSpace(question.Text) == "" {
					t.Fatalf("empty question: %+v", question)
				}
				if err := agent.ValidateOutput(agent.KindInterviewer, mustMarshal(t, session.Last())); err != nil {
					t.Fatalf("question output is not contract-valid: %v", err)
				}
				levels = append(levels, string(question.Level))
				if question.Clarification {
					clarifications++
				}
				if question.Fallback {
					fallbacks++
				}
				if answers := session.State().Answers; len(answers) < len(dialog.Answers) {
					if err := session.Answer(dialog.Answers[len(answers)]); err != nil {
						t.Fatalf("Answer: %v", err)
					}
				}
			}
			state := session.Close()

			if len(levels) != dialog.Expect.Questions {
				t.Fatalf("asked %d questions, want %d: %v", len(levels), dialog.Expect.Questions, levels)
			}
			if got, want := strings.Join(levels, ","), strings.Join(dialog.Expect.Levels, ","); got != want {
				t.Fatalf("question levels = %s, want %s", got, want)
			}
			if clarifications != dialog.Expect.Clarifications {
				t.Fatalf("clarifications = %d, want %d", clarifications, dialog.Expect.Clarifications)
			}
			if fallbacks != dialog.Expect.Fallbacks {
				t.Fatalf("fallbacks = %d, want %d", fallbacks, dialog.Expect.Fallbacks)
			}
			if client.failures != dialog.Expect.ModelErrors {
				t.Fatalf("model errors = %d, want %d", client.failures, dialog.Expect.ModelErrors)
			}
			if client.failures > 0 {
				if session.LastError() == nil {
					t.Fatal("degraded session must expose LastError")
				}
				if state.Status != domain.SessionDegraded {
					t.Fatalf("session status = %q, want degraded", state.Status)
				}
			}
			if _, err := session.Next(context.Background()); !errors.Is(err, ErrSessionDone) {
				t.Fatalf("Next after done error = %v, want ErrSessionDone", err)
			}
		})
	}
}

func TestModelErrorDoesNotBreakDialog(t *testing.T) {
	client := &scriptedClient{t: t}
	ag, err := New(client, WithMaxRepairs(0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := NewSession(ag, "sess-degraded", domain.Respondent{
		ID: "resp-1", Role: "Аналитик данных", Profession: "Аналитик данных",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	seen := map[domain.QuestionLevel]bool{}
	for !session.Done() {
		question, err := session.Next(context.Background())
		if err != nil {
			t.Fatalf("Next returned error on model failure: %v", err)
		}
		if !question.Fallback {
			t.Fatalf("question is not marked fallback: %+v", question)
		}
		if !errors.Is(session.LastError(), ErrModel) {
			t.Fatalf("LastError = %v, want ErrModel", session.LastError())
		}
		seen[question.Level] = true
		if err := session.Answer("Каждый день пишу SQL-запросы для отчётов"); err != nil {
			t.Fatalf("Answer: %v", err)
		}
	}
	state := session.Close()
	if state.Status != domain.SessionDegraded {
		t.Fatalf("status = %q, want degraded", state.Status)
	}
	for _, level := range []domain.QuestionLevel{domain.QuestionRole, domain.QuestionFunction, domain.QuestionTask, domain.QuestionAction} {
		if !seen[level] {
			t.Fatalf("fallback plan misses level %q", level)
		}
	}
}

func TestRepairInvalidJSON(t *testing.T) {
	valid := `{"question":{"id":"q-1","level":"role","text":"Чем вы занимаетесь?"},"rationale":"роль","is_clarification":false}`
	client := &scriptedClient{t: t, responses: []dialogResponse{
		{Kind: "invalid", Text: "не json"},
		{Kind: "answer", Text: valid},
	}}
	ag, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := ag.AskNext(context.Background(), agent.InterviewerInput{
		SessionID: "s-1", NextLevel: domain.QuestionRole,
	})
	if err != nil {
		t.Fatalf("AskNext after repair: %v", err)
	}
	if out.Question.Text != "Чем вы занимаетесь?" {
		t.Fatalf("question = %q", out.Question.Text)
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want 2 (invalid + repaired)", client.calls)
	}
}

func TestNewRequiresClient(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) accepted, want error")
	}
}

func TestNewSessionRequiresAgentAndID(t *testing.T) {
	if _, err := NewSession(nil, "s-1", domain.Respondent{}); err == nil {
		t.Fatal("NewSession(nil) accepted, want error")
	}
	ag, err := New(&scriptedClient{t: t})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := NewSession(ag, "  ", domain.Respondent{}); err == nil {
		t.Fatal("NewSession with empty id accepted, want error")
	}
}

func TestAnswerWithoutQuestion(t *testing.T) {
	ag, err := New(&scriptedClient{t: t})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session, err := NewSession(ag, "s-1", domain.Respondent{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := session.Answer("ответ без вопроса"); !errors.Is(err, ErrNoOpenQuestion) {
		t.Fatalf("Answer error = %v, want ErrNoOpenQuestion", err)
	}
}

func TestIsVague(t *testing.T) {
	tests := []struct {
		answer string
		vague  bool
	}{
		{"", true},
		{"Да", true},
		{"Ну, всякое бывает, по-разному", true},
		{"Иногда что-то делаю с отчётами", true},
		{"Каждый день пишу SQL-запросы для отчётов по продажам", false},
		{"Строю дашборды в Superset и проверяю корректность показателей", false},
	}
	for _, test := range tests {
		if got := IsVague(test.answer); got != test.vague {
			t.Fatalf("IsVague(%q) = %v, want %v", test.answer, got, test.vague)
		}
	}
}
