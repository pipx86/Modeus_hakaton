package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMessageStore имитирует durable-хранилище диалога и умеет имитировать
// недоступность базы.
type fakeMessageStore struct {
	mu        sync.Mutex
	messages  map[string][]Message
	appendErr error
	appends   int
}

func newFakeMessageStore() *fakeMessageStore {
	return &fakeMessageStore{messages: make(map[string][]Message)}
}

func (s *fakeMessageStore) AppendMessage(_ context.Context, runID string, message Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendErr != nil {
		return s.appendErr
	}
	s.appends++
	for _, existing := range s.messages[runID] {
		if existing.Sequence == message.Sequence {
			return nil
		}
	}
	s.messages[runID] = append(s.messages[runID], message)
	return nil
}

func (s *fakeMessageStore) ListMessages(_ context.Context, runID string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendErr != nil {
		return nil, s.appendErr
	}
	stored, ok := s.messages[runID]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]Message, len(stored))
	copy(out, stored)
	return out, nil
}

func (s *fakeMessageStore) setAppendErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendErr = err
}

func (s *fakeMessageStore) appendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appends
}

func startPersistentService(t *testing.T, store MessageStore, runID, profession string) *Service {
	t.Helper()
	service := NewService(fakeInterviewer{}, nil)
	service.SetMessageStore(store)
	if err := service.Start(runID, profession); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return service
}

func sameMessage(a, b Message) bool {
	return a.ID == b.ID &&
		a.Role == b.Role &&
		a.Content == b.Content &&
		a.Sequence == b.Sequence &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		len(a.Citations) == len(b.Citations)
}

func TestServicePersistsGreetingQuestionAndAnswer(t *testing.T) {
	store := newFakeMessageStore()
	service := startPersistentService(t, store, "run-persist", "Аналитик данных")

	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-persist")
		return err == nil && len(messages) >= 2
	})
	if _, err := service.Post("run-persist", "Я аналитик данных, отвечаю за отчётность."); err != nil {
		t.Fatalf("Post: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-persist")
		return err == nil && len(messages) >= 4
	})

	memory, err := service.Messages("run-persist")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	stored, err := store.ListMessages(context.Background(), "run-persist")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(stored) != len(memory) {
		t.Fatalf("stored %d messages, memory has %d", len(stored), len(memory))
	}
	wantRoles := []string{"agent", "agent", "user", "agent"}
	for index, want := range wantRoles {
		if index >= len(stored) {
			t.Fatalf("stored %d messages, want %d", len(stored), len(wantRoles))
		}
		if stored[index].Role != want {
			t.Fatalf("stored[%d].Role = %q, want %q", index, stored[index].Role, want)
		}
		if stored[index].Sequence != index+1 {
			t.Fatalf("stored[%d].Sequence = %d, want %d", index, stored[index].Sequence, index+1)
		}
		if stored[index].ID != memory[index].ID || stored[index].Content != memory[index].Content || !stored[index].CreatedAt.Equal(memory[index].CreatedAt) {
			t.Fatalf("stored[%d] = %+v, memory = %+v", index, stored[index], memory[index])
		}
	}
	if stored[0].Content != welcomeMessage {
		t.Fatalf("first stored message = %q, want the greeting", stored[0].Content)
	}
	if stored[2].Content != "Я аналитик данных, отвечаю за отчётность." {
		t.Fatalf("user answer was not persisted: %+v", stored[2])
	}
	if got := store.appendCount(); got != len(stored) {
		t.Fatalf("AppendMessage called %d times for %d stored messages", got, len(stored))
	}
}

func TestServiceReloadsHistoryWithoutInMemorySession(t *testing.T) {
	store := newFakeMessageStore()
	first := startPersistentService(t, store, "run-restart", "Аналитик данных")
	waitFor(t, time.Second, func() bool {
		messages, err := first.Messages("run-restart")
		return err == nil && len(messages) >= 2
	})
	if _, err := first.Post("run-restart", "Я готовлю управленческую отчётность."); err != nil {
		t.Fatalf("Post: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := first.Messages("run-restart")
		return err == nil && len(messages) >= 4
	})
	before, err := first.Messages("run-restart")
	if err != nil {
		t.Fatalf("Messages before restart: %v", err)
	}

	// Новый процесс: in-memory сессий нет, история должна прийти из хранилища.
	restarted := NewService(fakeInterviewer{}, nil)
	restarted.SetMessageStore(store)
	after, err := restarted.Messages("run-restart")
	if err != nil {
		t.Fatalf("Messages after restart: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("after restart %d messages, want %d", len(after), len(before))
	}
	for index := range before {
		if !sameMessage(before[index], after[index]) {
			t.Fatalf("message %d changed after restart: before %+v, after %+v", index, before[index], after[index])
		}
	}
	if _, err := restarted.Messages("run-unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run: err = %v, want ErrNotFound", err)
	}
}

func TestServiceDoesNotAcknowledgeUnpersistedAnswer(t *testing.T) {
	store := newFakeMessageStore()
	service := startPersistentService(t, store, "run-failure", "Аналитик данных")
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-failure")
		return err == nil && len(messages) >= 2
	})
	before, err := service.Messages("run-failure")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}

	store.setAppendErr(errors.New("database unavailable"))
	if _, err := service.Post("run-failure", "Ответ, который не должен сохраниться."); err == nil {
		t.Fatal("Post succeeded although persistence failed")
	}
	after, err := service.Messages("run-failure")
	if err != nil {
		t.Fatalf("Messages after failed Post: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("failed Post left %d messages in memory, want %d", len(after), len(before))
	}

	store.setAppendErr(nil)
	message, err := service.Post("run-failure", "Повторный ответ, который сохранится.")
	if err != nil {
		t.Fatalf("retried Post: %v", err)
	}
	wantID := fmt.Sprintf("msg-%s-%d", "run-failure", len(before)+1)
	if message.ID != wantID {
		t.Fatalf("retried message ID = %q, want %q", message.ID, wantID)
	}
}

func TestServicePersistsResearchResultMessage(t *testing.T) {
	store := newFakeMessageStore()
	service := startPersistentService(t, store, "run-final", "Аналитик данных")
	for i := 0; i < 4; i++ {
		waitFor(t, 3*time.Second, func() bool {
			messages, err := service.Messages("run-final")
			return err == nil && len(messages) >= 2*i+2
		})
		if _, err := service.Post("run-final", "Конкретный ответ про аналитику данных."); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	waitFor(t, 3*time.Second, func() bool {
		stored, err := store.ListMessages(context.Background(), "run-final")
		if err != nil || len(stored) == 0 {
			return false
		}
		last := stored[len(stored)-1]
		return last.Role == "agent" && strings.Contains(last.Content, "Интервью завершено") && strings.Contains(last.Content, "«Дерево»")
	})

	restarted := NewService(fakeInterviewer{}, nil)
	restarted.SetMessageStore(store)
	messages, err := restarted.Messages("run-final")
	if err != nil {
		t.Fatalf("Messages after restart: %v", err)
	}
	last := messages[len(messages)-1]
	if last.Role != "agent" || !strings.Contains(last.Content, "«Дерево»") {
		t.Fatalf("final research message was not persisted: %+v", last)
	}
}

func TestStoreNotConfiguredKeepsInMemoryBehaviour(t *testing.T) {
	service := NewService(nil, nil)
	if err := service.Start("run-memory-only", "Аналитик"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		messages, err := service.Messages("run-memory-only")
		return err == nil && len(messages) >= 2
	})
	if _, err := service.Messages("run-memory-only-unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run without store: err = %v, want ErrNotFound", err)
	}
}
