package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llmobs"
)

const runIDInterviewerAnswer = `{"question":{"id":"q","level":"role","text":"Расскажите о вашей роли."},"rationale":"Старт интервью.","is_clarification":false}`

// runIDInterviewer records the run id of every interviewer call.
type runIDInterviewer struct {
	mu     sync.Mutex
	runIDs []string
}

func (c *runIDInterviewer) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	c.mu.Lock()
	c.runIDs = append(c.runIDs, llmobs.RunIDFromContext(ctx))
	c.mu.Unlock()
	return runIDInterviewerAnswer, nil
}

func (c *runIDInterviewer) capturedRunIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.runIDs...)
}

// TestServiceAttachesRunIDToLLMCalls verifies the interviewer calls and the
// finalizer call receive the run id in their context, which is how
// llmobs.Recorder attributes llm_requests to the run.
func TestServiceAttachesRunIDToLLMCalls(t *testing.T) {
	client := &runIDInterviewer{}
	finalizerRunIDs := make(chan string, 1)
	service := NewService(client, nil)
	service.SetFinalizer(func(ctx context.Context, _ string, _ string, _ domain.InterviewSession) (domain.RunStatus, error) {
		finalizerRunIDs <- llmobs.RunIDFromContext(ctx)
		return domain.RunDone, nil
	})

	const runID = "run-chat-context"
	if err := service.Start(runID, "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { return len(client.capturedRunIDs()) >= 1 })
	waitFor(t, 3*time.Second, func() bool {
		messages, err := service.Messages(runID)
		return err == nil && len(messages) >= 2
	})

	for i := 0; i < 4; i++ {
		waitFor(t, 3*time.Second, func() bool {
			messages, err := service.Messages(runID)
			return err == nil && len(messages) >= 2*i+2
		})
		if _, err := service.Post(runID, "Конкретный ответ с достаточным количеством слов."); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}

	select {
	case got := <-finalizerRunIDs:
		if got != runID {
			t.Fatalf("finalizer run id = %q, want %q", got, runID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("finalizer was not called")
	}

	for _, got := range client.capturedRunIDs() {
		if got != runID {
			t.Fatalf("interviewer run id = %q, want %q", got, runID)
		}
	}
}
