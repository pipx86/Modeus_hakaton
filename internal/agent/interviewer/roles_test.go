package interviewer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// roleAwareClient checks that the respondent's profession is actually passed to
// the model and returns a contract-valid question for the requested plan level.
type roleAwareClient struct {
	t      *testing.T
	role   string
	calls  int
	levels []domain.QuestionLevel
}

func (c *roleAwareClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	if len(messages) == 0 {
		c.t.Fatal("empty messages")
	}
	user := messages[len(messages)-1].Content
	if !strings.Contains(user, c.role) {
		c.t.Errorf("model input does not mention role %q: %s", c.role, user)
	}
	if c.calls >= len(c.levels) {
		c.t.Fatalf("unexpected extra model call %d", c.calls+1)
	}
	level := c.levels[c.calls]
	c.calls++
	question := fmt.Sprintf("Что важно знать про уровень %s в роли %s?", level, c.role)
	payload := map[string]any{
		"question": map[string]any{
			"id":    fmt.Sprintf("q-%d", c.calls),
			"level": string(level),
			"text":  question,
		},
		"rationale":        "Проверка роли.",
		"is_clarification": false,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	return string(data), nil
}

func TestInterviewerSupportsThreeRoles(t *testing.T) {
	roles := []string{
		"Аналитик данных",
		"DevOps-инженер",
		"Backend-разработчик",
	}
	levels := []domain.QuestionLevel{
		domain.QuestionRole,
		domain.QuestionFunction,
		domain.QuestionTask,
		domain.QuestionAction,
		domain.QuestionTask,
		domain.QuestionAction,
	}
	for _, role := range roles {
		role := role
		t.Run(role, func(t *testing.T) {
			client := &roleAwareClient{t: t, role: role, levels: levels}
			agentInstance, err := New(client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			sessionID := "sess-" + strings.NewReplacer(" ", "-", ".", "-").Replace(strings.ToLower(role))
			session, err := NewSession(agentInstance, sessionID, domain.Respondent{
				ID:         "resp-" + sessionID,
				Role:       role,
				Profession: role,
			})
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}

			for !session.Done() {
				question, err := session.Next(context.Background())
				if err != nil {
					t.Fatalf("Next: %v", err)
				}
				if question.Fallback {
					t.Fatalf("unexpected fallback for role %q: %+v", role, question)
				}
				if err := session.Answer("Каждый день выполняю конкретные рабочие задачи и проверяю результат."); err != nil {
					t.Fatalf("Answer: %v", err)
				}
			}
			state := session.Close()
			if state.Status != domain.SessionDone {
				t.Fatalf("status = %q, want done", state.Status)
			}
			if client.calls != len(levels) {
				t.Fatalf("model calls = %d, want %d", client.calls, len(levels))
			}
			if got, want := len(state.Questions), len(levels); got != want {
				t.Fatalf("questions = %d, want %d", got, want)
			}
			if err := agent.ValidateOutput(agent.KindInterviewer, mustMarshal(t, session.Last())); err != nil {
				t.Fatalf("last output is invalid: %v", err)
			}
		})
	}
}
