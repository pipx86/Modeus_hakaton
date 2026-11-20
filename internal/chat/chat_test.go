package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

type fakeInterviewer struct {
	answer string
}

func (f fakeInterviewer) Chat(context.Context, []llm.Message) (string, error) {
	if f.answer != "" {
		return f.answer, nil
	}
	return `{"question":{"id":"q","level":"role","text":"Расскажите о вашей роли."},"rationale":"Старт интервью.","is_clarification":false}`, nil
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met in time")
}

func TestServiceStartsAndPosts(t *testing.T) {
	finished := make(chan domain.RunStatus, 1)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})

	if err := service.Start("run-1", "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-1")
		return err == nil && len(messages) >= 2 && messages[0].Role == "agent"
	})

	message, err := service.Post("run-1", "Я аналитик данных, отвечаю за отчётность.")
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if message.Role != "user" || message.Content == "" {
		t.Fatalf("unexpected user message: %+v", message)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-1")
		return err == nil && len(messages) >= 4
	})
}

func TestServicePassesGitHubProfileToBothInterviews(t *testing.T) {
	for _, test := range []struct {
		name  string
		start func(*Service, string, string) error
	}{
		{name: "chat", start: (*Service).Start},
		{name: "learning", start: (*Service).StartLearning},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(fakeInterviewer{}, nil)
			service.SetProfileContext("run-profile", "GitHub @octocat: Go, PostgreSQL")
			if err := test.start(service, "run-profile", "Backend-разработчик"); err != nil {
				t.Fatalf("start: %v", err)
			}
			waitFor(t, time.Second, func() bool {
				service.mu.Lock()
				session := service.sessions["run-profile"]
				service.mu.Unlock()
				if session == nil {
					return false
				}
				session.mu.Lock()
				defer session.mu.Unlock()
				return session.session.State().ProfileContext != ""
			})
			service.mu.Lock()
			session := service.sessions["run-profile"]
			service.mu.Unlock()
			session.mu.Lock()
			defer session.mu.Unlock()
			if got := session.session.State().ProfileContext; got != "GitHub @octocat: Go, PostgreSQL" {
				t.Fatalf("profile = %q", got)
			}
		})
	}
}

func TestServiceFallsBackWithoutProvider(t *testing.T) {
	service := NewService(nil, nil)
	if err := service.Start("run-2", "Аналитик"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-2")
		return err == nil && len(messages) >= 2 && messages[0].Role == "agent"
	})
}

func TestServiceFinishesAfterPlan(t *testing.T) {
	finished := make(chan domain.RunStatus, 1)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	if err := service.Start("run-3", "Аналитик"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		messages, err := service.Messages("run-3")
		return err == nil && len(messages) >= 2
	})
	for i := 0; i < 6; i++ {
		waitFor(t, 3*time.Second, func() bool {
			messages, err := service.Messages("run-3")
			return err == nil && len(messages) >= 2*i+2
		})
		if _, err := service.Post("run-3", "Конкретный ответ с достаточным количеством слов."); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
		if i < 5 {
			next := 2*i + 4
			waitFor(t, 3*time.Second, func() bool {
				messages, err := service.Messages("run-3")
				return err == nil && len(messages) >= next
			})
		}
	}
	select {
	case status := <-finished:
		if status != domain.RunDone {
			t.Fatalf("status = %q, want done", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("finish callback was not called")
	}
}

func TestSubscribeReceivesEvents(t *testing.T) {
	service := NewService(fakeInterviewer{}, nil)
	if err := service.Start("run-4", "Аналитик"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-4")
		return err == nil && len(messages) >= 2
	})
	events, cancel, err := service.Subscribe("run-4")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()

	if _, err := service.Post("run-4", "Я аналитик данных и отвечаю за отчётность."); err != nil {
		t.Fatalf("Post: %v", err)
	}
	select {
	case event := <-events:
		if event.Type != "agent_thinking" && event.Type != "agent_message" {
			t.Fatalf("unexpected event type %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestErrors(t *testing.T) {
	service := NewService(nil, nil)
	if _, err := service.Post("missing", "text"); err != ErrNotFound {
		t.Fatalf("Post missing = %v, want ErrNotFound", err)
	}
	if _, err := service.Messages("missing"); err != ErrNotFound {
		t.Fatalf("Messages missing = %v, want ErrNotFound", err)
	}
	if err := service.Start("run-5", "Аналитик"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := service.Post("run-5", ""); err != ErrEmptyMessage {
		t.Fatalf("Post empty = %v, want ErrEmptyMessage", err)
	}
}

func TestServiceCallsFinalizerAfterInterview(t *testing.T) {
	called := make(chan domain.InterviewSession, 1)
	service := NewService(fakeInterviewer{}, nil)
	service.SetFinalizer(func(_ context.Context, _ string, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
		if profession != "Аналитик" {
			t.Errorf("profession = %q", profession)
		}
		called <- session
		return domain.RunDone, nil
	})
	if err := service.Start("run-6", "Аналитик"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 6; i++ {
		waitFor(t, time.Second, func() bool {
			messages, err := service.Messages("run-6")
			return err == nil && len(messages) >= 2*i+2
		})
		if _, err := service.Post("run-6", "Конкретный ответ про аналитику данных."); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	select {
	case session := <-called:
		if len(session.Questions) == 0 || len(session.Answers) == 0 {
			t.Fatalf("finalizer got empty session: %+v", session)
		}
		if got := transcriptFromSessionForTest(session); got == "" {
			t.Fatal("transcript is empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finalizer was not called")
	}
}

func TestServicePostsFinalCourses(t *testing.T) {
	service := NewService(fakeInterviewer{}, nil)
	service.SetFinalizer(func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
		return domain.RunDone, nil
	})
	if err := service.Start("run-7", "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 6; i++ {
		waitFor(t, 3*time.Second, func() bool {
			messages, err := service.Messages("run-7")
			return err == nil && len(messages) >= 2*i+2
		})
		if _, err := service.Post("run-7", "Конкретный ответ про аналитику данных."); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	waitFor(t, 3*time.Second, func() bool {
		messages, err := service.Messages("run-7")
		if err != nil || len(messages) == 0 {
			return false
		}
		last := messages[len(messages)-1].Content
		return strings.Contains(last, "Интервью завершено") && strings.Contains(last, "https://stepik.org/course/300883/promo")
	})
}

func transcriptFromSessionForTest(session domain.InterviewSession) string {
	if len(session.Questions) == 0 || len(session.Answers) == 0 {
		return ""
	}
	return session.Questions[0].Text + session.Answers[0].Text
}
