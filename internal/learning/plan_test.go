package learning

import (
	"context"
	"errors"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type fakeClient struct {
	answer string
	prompt string
	err    error
}

func (f *fakeClient) Chat(_ context.Context, messages []llm.Message) (string, error) {
	if len(messages) > 0 {
		f.prompt = messages[0].Content
	}
	return f.answer, f.err
}

func TestGenerateParsesPlan(t *testing.T) {
	client := &fakeClient{answer: "```json\n" + `{
		"summary": "Путь в аналитику",
		"weeks": 10,
		"stages": [
			{"title": "SQL", "goal": "Писать запросы", "topics": ["SELECT", "JOIN"], "practice": ["Собрать отчёт"]}
		]
	}` + "\n```"}
	plan, err := Generate(context.Background(), client, "Аналитик данных", []domain.Source{
		{Title: "Вакансия", Content: "Нужен SQL и Excel"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if plan.Profession != "Аналитик данных" || len(plan.Stages) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Stages[0].Title != "SQL" || len(plan.Stages[0].Topics) != 2 {
		t.Fatalf("unexpected stage: %+v", plan.Stages[0])
	}
	if !strings.Contains(client.prompt, "Нужен SQL и Excel") {
		t.Fatalf("source context missing from prompt: %q", client.prompt)
	}
	if !strings.Contains(client.prompt, "только на русском") {
		t.Fatalf("language instruction missing from prompt")
	}
}

func TestGenerateRejectsMalformedPlan(t *testing.T) {
	client := &fakeClient{answer: "not json"}
	if _, err := Generate(context.Background(), client, "Аналитик", nil); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestGenerateUsesInterviewBackground(t *testing.T) {
	client := &fakeClient{answer: `{"summary":"x","weeks":8,"stages":[{"title":"Kubernetes","goal":"y","topics":["pods"],"practice":["deploy"]}]}`}
	plan, err := GenerateWithBackground(
		context.Background(),
		client,
		"DevOps-инженер",
		nil,
		"Пользователь уже знает Docker и Git, но не знает Kubernetes.",
	)
	if err != nil {
		t.Fatalf("GenerateWithBackground: %v", err)
	}
	if len(plan.Stages) != 1 || plan.Stages[0].Title != "Kubernetes" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if !strings.Contains(client.prompt, "Docker") || !strings.Contains(client.prompt, "не знает Kubernetes") {
		t.Fatalf("background not included in prompt: %q", client.prompt)
	}
}

func TestGenerateNeedsClientAndStages(t *testing.T) {
	if _, err := Generate(context.Background(), nil, "Аналитик", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client: %v", err)
	}
	client := &fakeClient{answer: `{"summary":"x","stages":[]}`}
	if _, err := Generate(context.Background(), client, "Аналитик", nil); err == nil {
		t.Fatal("expected empty stages error")
	}
}
