package pipeline

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

var nextLevelRe = regexp.MustCompile(`"next_level"\s*:\s*"([a-z]+)"`)

type roleFakeClient struct {
	calls int
}

func (f *roleFakeClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	f.calls++
	if len(messages) == 0 {
		return "", errors.New("fake: empty messages")
	}
	system := strings.ToLower(messages[0].Content)
	switch {
	case strings.Contains(system, "аналитик"):
		return analystFixture, nil
	case strings.Contains(system, "интервьюер"):
		level := "role"
		if m := nextLevelRe.FindStringSubmatch(messages[len(messages)-1].Content); len(m) == 2 {
			level = m[1]
		}
		return `{"question":{"id":"q","level":"` + level + `","text":"Расскажите о конкретной задаче."},"rationale":"План интервью.","is_clarification":false}`, nil
	default:
		return "", errors.New("fake: unknown agent prompt")
	}
}

const analystFixture = `{
  "entities": [
    {
      "id": "role-1", "type": "role", "title": "Аналитик данных", "confidence": 0.95,
      "evidence": [{"id": "ev-role-1", "source_id": "src-interview", "ref_id": "answer-1", "quote": "Я аналитик данных, строю дашборды, готовлю отчёт по воронке и выгружаю данные SQL-запросами."}]
    },
    {
      "id": "function-1", "type": "function", "title": "Отчётность", "confidence": 0.9,
      "evidence": [{"id": "ev-function-1", "source_id": "src-interview", "ref_id": "answer-1", "quote": "Я аналитик данных, строю дашборды, готовлю отчёт по воронке и выгружаю данные SQL-запросами."}]
    },
    {
      "id": "task-1", "type": "task", "title": "Готовить отчёт", "confidence": 0.9,
      "evidence": [{"id": "ev-task-1", "source_id": "src-interview", "ref_id": "answer-1", "quote": "Я аналитик данных, строю дашборды, готовлю отчёт по воронке и выгружаю данные SQL-запросами."}]
    },
    {
      "id": "action-1", "type": "action", "title": "Писать SQL", "confidence": 0.85,
      "evidence": [{"id": "ev-action-1", "source_id": "src-interview", "ref_id": "answer-1", "quote": "Я аналитик данных, строю дашборды, готовлю отчёт по воронке и выгружаю данные SQL-запросами."}]
    }
  ]
}`

func TestRunThreeAgentPipeline(t *testing.T) {
	client := &roleFakeClient{}
	result, err := Run(context.Background(), Config{
		Client:     client,
		Profession: "Аналитик данных",
		Respondent: domain.Respondent{ID: "r1", Role: "Аналитик данных", Profession: "Аналитик данных"},
		Answers: func(domain.Question) string {
			return "Я аналитик данных, строю дашборды, готовлю отчёт по воронке и выгружаю данные SQL-запросами."
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Run.Status != domain.RunDone && result.Run.Status != domain.RunDegraded {
		t.Fatalf("run status = %q, want done or degraded", result.Run.Status)
	}
	if len(result.Synthesis.Tree) == 0 {
		t.Fatal("synthesis tree is empty")
	}
	if result.Transcript == "" || len(result.Session.Questions) == 0 {
		t.Fatal("interview transcript/questions are empty")
	}
	if result.Analyst.Entities == nil {
		t.Fatal("analyst output is nil")
	}
	if client.calls == 0 {
		t.Fatal("fake model was not called")
	}
}

func TestRunRejectsMissingConfig(t *testing.T) {
	if _, err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("empty config: want error")
	}
	if _, err := Run(nil, Config{}); err == nil {
		t.Fatal("nil context: want error")
	}
}
