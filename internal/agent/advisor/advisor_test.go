package advisor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"activity-descriptor/internal/llm"
)

type fakeClient struct {
	answer string
	err    error
	calls  int
}

func (f *fakeClient) Chat(context.Context, []llm.Message) (string, error) {
	f.calls++
	return f.answer, f.err
}

const validAnswer = `{
  "level": "junior",
  "summary": "Вы начинаете путь аналитика данных: есть базовые отчёты, но не хватает SQL и BI-практики.",
  "courses": [
    {"title": "SQL с нуля", "url": "https://stepik.org/course/300883/promo", "reason": "закрывает базу SQL"},
    {"title": "BI для аналитика", "url": "https://stepik.org/course/266132/promo", "reason": "даёт практику визуализации"}
  ],
  "plan": [
    {"title": "SQL", "description": "пройти SELECT, JOIN, GROUP BY"},
    {"title": "BI", "description": "собрать дашборд"},
    {"title": "Портфолио", "description": "оформить 2 кейса"},
    {"title": "Практика", "description": "решить реальную задачу"},
    {"title": "Резюме", "description": "описать результаты"}
  ]
}`

func TestRecommendValid(t *testing.T) {
	client := &fakeClient{answer: validAnswer}
	agent := New(client)
	rec, err := agent.Recommend(context.Background(), Input{
		Profession: "Аналитик данных (junior)",
		Transcript: "Вопрос: чем занимаетесь?\nОтвет: готовлю отчёты.",
	})
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if rec.Level != "junior" || len(rec.Courses) != 2 || len(rec.Plan) != 5 {
		t.Fatalf("unexpected recommendation: %+v", rec)
	}
	if client.calls != 1 {
		t.Fatalf("calls = %d, want 1", client.calls)
	}
	message := rec.ChatMessage()
	for _, want := range []string{"Рекомендации по итогам интервью", "SQL с нуля", "https://stepik.org/course/300883/promo", "План развития"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q:\n%s", want, message)
		}
	}
}

func TestRecommendStripsCodeFence(t *testing.T) {
	agent := New(&fakeClient{answer: "```json\n" + validAnswer + "\n```"})
	if _, err := agent.Recommend(context.Background(), Input{Transcript: "ответ"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
}

func TestRecommendRejectsInvalidOutput(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "not json", raw: "sorry"},
		{name: "empty summary", raw: `{"level":"junior","summary":"","courses":[{"title":"a","url":"https://stepik.org/course/300883/promo"}],"plan":[]}`},
		{name: "one course", raw: `{"level":"junior","summary":"x","courses":[{"title":"a","url":"https://stepik.org/course/300883/promo"}],"plan":[{"title":"a"},{"title":"b"},{"title":"c"},{"title":"d"}]}`},
		{name: "unknown course", raw: `{"level":"junior","summary":"x","courses":[{"title":"a","url":"https://evil.example/course"},{"title":"b","url":"https://stepik.org/course/300883/promo"}],"plan":[{"title":"a"},{"title":"b"},{"title":"c"},{"title":"d"}]}`},
		{name: "too few plan steps", raw: `{"level":"junior","summary":"x","courses":[{"title":"a","url":"https://stepik.org/course/300883/promo"},{"title":"b","url":"https://stepik.org/course/266132/promo"}],"plan":[{"title":"a"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(&fakeClient{answer: tc.raw}).Recommend(context.Background(), Input{Transcript: "ответ"})
			if err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestRecommendErrors(t *testing.T) {
	if _, err := New(nil).Recommend(context.Background(), Input{Transcript: "x"}); err == nil {
		t.Fatal("nil client: want error")
	}
	if _, err := New(&fakeClient{answer: validAnswer}).Recommend(context.Background(), Input{}); err == nil {
		t.Fatal("empty transcript: want error")
	}
	//lint:ignore contextcheck the nil context is the point of this check
	if _, err := New(&fakeClient{answer: validAnswer}).Recommend(nil, Input{Transcript: "x"}); err == nil {
		t.Fatal("nil context: want error")
	}
	failing := &fakeClient{err: errors.New("provider down")}
	if _, err := New(failing).Recommend(context.Background(), Input{Transcript: "x"}); err == nil {
		t.Fatal("client error: want error")
	}
}
