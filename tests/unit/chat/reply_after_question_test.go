package chat_test

import (
	"context"
	"testing"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type interviewerFixture struct{}

func (interviewerFixture) Chat(context.Context, []llm.Message) (string, error) {
	return `{"question":{"id":"q","level":"role","text":"Расскажите о вашей роли."},"rationale":"Интервью.","is_clarification":false}`, nil
}

func TestNextQuestionAfterImmediateReplyToSSE(t *testing.T) {
	finished := make(chan domain.RunStatus, 1)
	service := chat.NewService(interviewerFixture{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	const run = "test-immediate-answer"
	if err := service.Start(run, "Аналитик данных"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		messages, err := service.Messages(run)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("первый вопрос не появился")
		case <-tick.C:
		}
	}

	events, unsubscribe, err := service.Subscribe(run)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	for answer := 1; answer <= 6; answer++ {
		if _, err := service.Post(run, "Формирую отчёты и проверяю согласованность данных."); err != nil {
			t.Fatalf("ответ %d: %v", answer, err)
		}
		if answer == 6 {
			break
		}
	waitForQuestion:
		for {
			select {
			case <-ctx.Done():
				t.Fatalf("после ответа %d не появился следующий вопрос", answer)
			case event := <-events:
				if event.Type == "agent_message" && event.Message != nil && event.Message.Role == "agent" {
					break waitForQuestion
				}
			}
		}
	}
	select {
	case <-ctx.Done():
		t.Fatal("интервью не завершилось после всех ответов")
	case status := <-finished:
		if status != domain.RunDone {
			t.Fatalf("статус интервью: %s", status)
		}
	}
}
