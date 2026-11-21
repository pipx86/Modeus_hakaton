package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// fakeSessionStore is an in-memory chat.SessionStore that mimics the
// transactional and optimistic-version behaviour of the PostgreSQL adapter.
type fakeSessionStore struct {
	mu       sync.Mutex
	runs     map[string]domain.RunStatus
	sessions map[string]SessionSnapshot
	messages map[string][]Message
	trees    map[string]bool
	saveErr  error
	saves    int
	appends  int
}

func TestMessagesProbeDoesNotFailBatchRun(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("batch-run", domain.RunRunning)
	finished := make(chan domain.RunStatus, 1)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	service.SetSessionStore(store)

	messages, err := service.Messages("batch-run")
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("batch messages = %d, want 0", len(messages))
	}
	select {
	case status := <-finished:
		t.Fatalf("batch run was marked %s by a messages probe", status)
	default:
	}
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		runs:     make(map[string]domain.RunStatus),
		sessions: make(map[string]SessionSnapshot),
		messages: make(map[string][]Message),
		trees:    make(map[string]bool),
	}
}

func (s *fakeSessionStore) seedRun(runID string, status domain.RunStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[runID] = status
}

func (s *fakeSessionStore) setSaveErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveErr = err
}

func (s *fakeSessionStore) bumpVersion(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.sessions[runID]
	snapshot.Version++
	s.sessions[runID] = snapshot
}

func (s *fakeSessionStore) appendMessageLocked(runID string, message Message) {
	for _, existing := range s.messages[runID] {
		if existing.Sequence == message.Sequence {
			return
		}
	}
	s.messages[runID] = append(s.messages[runID], message)
	s.appends++
}

func (s *fakeSessionStore) AppendMessage(_ context.Context, runID string, message Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return fmt.Errorf("%w: run id %q", ErrNotFound, runID)
	}
	s.appendMessageLocked(runID, message)
	return nil
}

func (s *fakeSessionStore) ListMessages(_ context.Context, runID string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return nil, fmt.Errorf("%w: run id %q", ErrNotFound, runID)
	}
	stored := s.messages[runID]
	out := make([]Message, len(stored))
	copy(out, stored)
	return out, nil
}

func (s *fakeSessionStore) LoadSession(_ context.Context, runID string) (SessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.sessions[runID]
	if !ok {
		return SessionSnapshot{}, fmt.Errorf("%w: run id %q", ErrNotFound, runID)
	}
	return snapshot, nil
}

func (s *fakeSessionStore) SaveStep(_ context.Context, runID string, snapshot SessionSnapshot, messages []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	if _, ok := s.runs[runID]; !ok {
		return fmt.Errorf("%w: run id %q", ErrNotFound, runID)
	}
	current, exists := s.sessions[runID]
	if snapshot.Version == 0 {
		if exists {
			return ErrVersionConflict
		}
		snapshot.Version = 1
	} else {
		if !exists || current.Version != snapshot.Version {
			return ErrVersionConflict
		}
		snapshot.Version++
	}
	s.sessions[runID] = snapshot
	for _, message := range messages {
		s.appendMessageLocked(runID, message)
	}
	s.saves++
	return nil
}

func (s *fakeSessionStore) RunStatus(_ context.Context, runID string) (domain.RunStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.runs[runID]
	if !ok {
		return "", fmt.Errorf("%w: run id %q", ErrNotFound, runID)
	}
	return status, nil
}

func (s *fakeSessionStore) RunHasTree(_ context.Context, runID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trees[runID], nil
}

func (s *fakeSessionStore) ListSessionsByStatus(_ context.Context, status SessionStatus, _ time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var runIDs []string
	for runID, snapshot := range s.sessions {
		if snapshot.Status == status {
			runIDs = append(runIDs, runID)
		}
	}
	return runIDs, nil
}

func (s *fakeSessionStore) ListFinalizedSessionsWithActiveRuns(context.Context, time.Time) ([]FinalizedSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FinalizedSession
	for runID, snapshot := range s.sessions {
		if snapshot.Status != SessionCompleted && snapshot.Status != SessionFailed {
			continue
		}
		status := s.runs[runID]
		if status == domain.RunPending || status == domain.RunRunning {
			out = append(out, FinalizedSession{RunID: runID, FinalStatus: snapshot.FinalStatus})
		}
	}
	return out, nil
}

func (s *fakeSessionStore) ListInterruptedChatRuns(context.Context, time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var runIDs []string
	for runID, status := range s.runs {
		if status != domain.RunPending && status != domain.RunRunning {
			continue
		}
		if _, hasSession := s.sessions[runID]; !hasSession {
			runIDs = append(runIDs, runID)
		}
	}
	return runIDs, nil
}

func (s *fakeSessionStore) snapshotOf(runID string) (SessionSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.sessions[runID]
	return snapshot, ok
}

func (s *fakeSessionStore) messageCount(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages[runID])
}

// seedDialog builds a real interviewer snapshot and matching message history so
// tests can start from any cursor without driving the whole dialog through the
// service. closed=true closes the session; terminalStatus adds the final message
// and marks the session completed.
func seedDialog(t *testing.T, store *fakeSessionStore, runID, profession string, questions, answers int, closed bool, status SessionStatus) {
	t.Helper()
	agentInstance, err := interviewer.New(fakeInterviewer{})
	if err != nil {
		t.Fatalf("interviewer.New: %v", err)
	}
	session, err := interviewer.NewSession(agentInstance, "run-"+runID, domain.Respondent{
		ID:         "respondent-" + runID,
		Role:       profession,
		Profession: profession,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	messages := []Message{newMessage(runID, 1, "agent", welcomeMessage)}
	for index := 0; index < questions; index++ {
		question, err := session.Next(context.Background())
		if err != nil {
			t.Fatalf("Next %d: %v", index, err)
		}
		messages = append(messages, newMessage(runID, len(messages)+1, "agent", question.Text))
		if index < answers {
			const answer = "Конкретный ответ с достаточным количеством слов."
			if err := session.Answer(answer); err != nil {
				t.Fatalf("Answer %d: %v", index, err)
			}
			messages = append(messages, newMessage(runID, len(messages)+1, "user", answer))
		}
	}
	if closed {
		session.Close()
	}
	finalStatus := domain.RunStatus("")
	if status == SessionCompleted {
		finalStatus = domain.RunDone
		messages = append(messages, newMessage(runID, len(messages)+1, "agent", interviewer.FinalCoursesMessage(profession)))
	}
	if status == SessionFailed {
		finalStatus = domain.RunFailed
	}
	store.mu.Lock()
	store.sessions[runID] = SessionSnapshot{
		SchemaVersion: SessionSchemaVersion,
		Profession:    profession,
		Status:        status,
		FinalStatus:   finalStatus,
		Interviewer:   session.Snapshot(),
		Version:       1,
	}
	store.messages[runID] = messages
	store.mu.Unlock()
}

func waitForStoredMessages(t *testing.T, service *Service, runID string, want int) []Message {
	t.Helper()
	var last []Message
	waitFor(t, 5*time.Second, func() bool {
		messages, err := service.Messages(runID)
		if err != nil {
			return false
		}
		last = messages
		return len(messages) >= want
	})
	return last
}

// TestServiceRestoresInterviewAfterRestart simulates a backend restart in the
// middle of an interview and continues it to completion through a brand-new
// service instance backed by the same store.
func TestServiceRestoresInterviewAfterRestart(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-restart", domain.RunRunning)

	first := NewService(fakeInterviewer{}, nil)
	first.SetSessionStore(store)
	if err := first.Start("run-restart", "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForStoredMessages(t, first, "run-restart", 2)
	if _, err := first.Post("run-restart", "Я аналитик данных и отвечаю за управленческую отчётность."); err != nil {
		t.Fatalf("Post: %v", err)
	}
	waitForStoredMessages(t, first, "run-restart", 4)

	// New process: no in-memory sessions, everything comes from the store.
	finished := make(chan domain.RunStatus, 1)
	restarted := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	restarted.SetSessionStore(store)
	afterRestart := waitForStoredMessages(t, restarted, "run-restart", 4)
	if len(afterRestart) != 4 {
		t.Fatalf("after restart %d messages, want 4", len(afterRestart))
	}

	// Three answers remain: the restored session keeps its cursor and finishes.
	for index := 0; index < 3; index++ {
		waitForStoredMessages(t, restarted, "run-restart", 4+2*index)
		if _, err := restarted.Post("run-restart", "Конкретный ответ про аналитику данных."); err != nil {
			t.Fatalf("Post after restart %d: %v", index, err)
		}
	}
	final := waitForStoredMessages(t, restarted, "run-restart", 10)
	last := final[len(final)-1]
	if last.Role != "agent" || !contains(last.Content, "Интервью завершено") {
		t.Fatalf("final research message was not persisted: %+v", last)
	}
	select {
	case status := <-finished:
		if status != domain.RunDone {
			t.Fatalf("status = %q, want done", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onFinish was not called")
	}
	snapshot, ok := store.snapshotOf("run-restart")
	if !ok || snapshot.Status != SessionCompleted {
		t.Fatalf("stored session status = %q, want completed", snapshot.Status)
	}

	// Another restart serves the same complete dialog and refuses new answers.
	third := NewService(fakeInterviewer{}, nil)
	third.SetSessionStore(store)
	messages := waitForStoredMessages(t, third, "run-restart", 10)
	if len(messages) != 10 {
		t.Fatalf("after second restart %d messages, want 10", len(messages))
	}
	before := store.messageCount("run-restart")
	if _, err := third.Post("run-restart", "Ещё один ответ."); !errors.Is(err, ErrSessionFinished) {
		t.Fatalf("Post on finished run = %v, want ErrSessionFinished", err)
	}
	if after := store.messageCount("run-restart"); after != before {
		t.Fatalf("finished run accepted a message: %d -> %d", before, after)
	}
}

// TestRestoredSessionAsksPendingQuestionOnSubscribe covers a restart between the
// durable answer and the next question: the question must be asked exactly once,
// and only after a subscriber is registered so the event is delivered.
func TestRestoredSessionAsksPendingQuestionOnSubscribe(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-pending", domain.RunRunning)
	// One question asked, one answer stored, no next question yet.
	seedDialog(t, store, "run-pending", "Аналитик данных", 1, 1, false, SessionActive)

	service := NewService(fakeInterviewer{}, nil)
	service.SetSessionStore(store)
	if messages, err := service.Messages("run-pending"); err != nil || len(messages) != 3 {
		t.Fatalf("Messages = %d, %v; want 3 messages and no error", len(messages), err)
	}
	// GET alone must not generate the next question.
	time.Sleep(50 * time.Millisecond)
	if snapshot, _ := store.snapshotOf("run-pending"); len(snapshot.Interviewer.State.Questions) != 1 {
		t.Fatalf("Messages resumed the flow unexpectedly: %d questions", len(snapshot.Interviewer.State.Questions))
	}

	events, cancel, err := service.Subscribe("run-pending")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	waitFor(t, 5*time.Second, func() bool {
		snapshot, ok := store.snapshotOf("run-pending")
		return ok && len(snapshot.Interviewer.State.Questions) == 2
	})
	select {
	case event := <-events:
		if event.Type == "agent_message" && (event.Message == nil || event.Message.Role != "agent") {
			t.Fatalf("unexpected agent message event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no chat event after resuming the question flow")
	}
	if messages := waitForStoredMessages(t, service, "run-pending", 4); len(messages) != 4 {
		t.Fatalf("messages after resume = %d, want 4", len(messages))
	}

	// A second subscriber must not ask the same question again.
	second, cancelSecond, err := service.Subscribe("run-pending")
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	cancelSecond()
	_ = second
	time.Sleep(100 * time.Millisecond)
	snapshot, _ := store.snapshotOf("run-pending")
	if got := len(snapshot.Interviewer.State.Questions); got != 2 {
		t.Fatalf("question was asked again: %d questions", got)
	}

	// The restored dialog continues normally.
	if _, err := service.Post("run-pending", "Конкретный ответ про аналитику данных."); err != nil {
		t.Fatalf("Post after resume: %v", err)
	}
}

// TestRecoverSessionsCompletesFinalizingSession covers a restart after the last
// answer but before the post-interview pipeline finished.
func TestRecoverSessionsCompletesFinalizingSession(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-finalizing", domain.RunRunning)
	seedDialog(t, store, "run-finalizing", "Аналитик данных", 4, 4, true, SessionFinalizing)

	finalizerCalls := make(chan struct{}, 1)
	finished := make(chan domain.RunStatus, 1)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	service.SetSessionStore(store)
	service.SetFinalizer(func(context.Context, string, string, domain.InterviewSession) (domain.RunStatus, error) {
		finalizerCalls <- struct{}{}
		return domain.RunDone, nil
	})

	service.RecoverSessions(context.Background())
	select {
	case <-finalizerCalls:
	case <-time.After(5 * time.Second):
		t.Fatal("finalizer was not resumed after restart")
	}
	final := waitForStoredMessages(t, service, "run-finalizing", 10)
	last := final[len(final)-1]
	if last.Role != "agent" || !contains(last.Content, "«Дерево»") {
		t.Fatalf("final message missing after resumed finalization: %+v", last)
	}
	select {
	case status := <-finished:
		if status != domain.RunDone {
			t.Fatalf("status = %q, want done", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onFinish was not called")
	}
	snapshot, _ := store.snapshotOf("run-finalizing")
	if snapshot.Status != SessionCompleted {
		t.Fatalf("session status = %q, want completed", snapshot.Status)
	}
}

// TestCompletedRunIsReadOnlyAfterRestart also reconciles the run status when the
// process died between the terminal session write and the run status update.
func TestCompletedRunIsReadOnlyAfterRestart(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-done", domain.RunRunning)
	seedDialog(t, store, "run-done", "Аналитик данных", 4, 4, true, SessionCompleted)

	finished := make(chan domain.RunStatus, 1)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	service.SetSessionStore(store)
	messages := waitForStoredMessages(t, service, "run-done", 10)
	if len(messages) != 10 {
		t.Fatalf("messages = %d, want 10", len(messages))
	}
	select {
	case status := <-finished:
		if status != domain.RunDone {
			t.Fatalf("reconciled status = %q, want done", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run status was not reconciled")
	}
	before := store.messageCount("run-done")
	if _, err := service.Post("run-done", "Поздний ответ."); !errors.Is(err, ErrSessionFinished) {
		t.Fatalf("Post = %v, want ErrSessionFinished", err)
	}
	if after := store.messageCount("run-done"); after != before {
		t.Fatalf("read-only run accepted a message: %d -> %d", before, after)
	}
}

// gatedInterviewer answers the first question immediately and blocks every later
// call until release, making a concurrent POST/answer race deterministic.
type gatedInterviewer struct {
	calls   atomic.Int32
	release chan struct{}
}

func (g *gatedInterviewer) Chat(_ context.Context, _ []llm.Message) (string, error) {
	if g.calls.Add(1) > 1 {
		<-g.release
	}
	return `{"question":{"id":"q","level":"role","text":"Расскажите о вашей роли."},"rationale":"Старт интервью.","is_clarification":false}`, nil
}

func TestConcurrentPostsAdvanceInterviewOnce(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-concurrent", domain.RunRunning)
	client := &gatedInterviewer{release: make(chan struct{})}
	service := NewService(client, nil)
	service.SetSessionStore(store)
	if err := service.Start("run-concurrent", "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForStoredMessages(t, service, "run-concurrent", 2)

	const posters = 8
	var wg sync.WaitGroup
	errs := make(chan error, posters)
	for i := 0; i < posters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Post("run-concurrent", "Конкретный ответ про аналитику данных.")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	accepted, rejected := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrQuestionPending):
			rejected++
		default:
			t.Fatalf("unexpected Post error: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent answers, want exactly 1", accepted)
	}
	if rejected != posters-1 {
		t.Fatalf("rejected %d concurrent answers, want %d", rejected, posters-1)
	}

	// Release the interview: one more question arrives, then no duplicate answer
	// exists in the durable dialog.
	close(client.release)
	waitForStoredMessages(t, service, "run-concurrent", 4)
	store.mu.Lock()
	var userMessages int
	for _, message := range store.messages["run-concurrent"] {
		if message.Role == "user" {
			userMessages++
		}
	}
	store.mu.Unlock()
	if userMessages != 1 {
		t.Fatalf("stored %d user messages, want exactly 1", userMessages)
	}
}

// TestLegacyChatRunIsHonestlyFailed covers chat runs created before durable
// session snapshots: they cannot be resumed and must fail explicitly.
func TestLegacyChatRunIsHonestlyFailed(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-legacy", domain.RunRunning)
	store.mu.Lock()
	store.messages["run-legacy"] = []Message{newMessage("run-legacy", 1, "agent", welcomeMessage)}
	store.mu.Unlock()

	finished := make(chan domain.RunStatus, 4)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	service.SetSessionStore(store)
	if _, err := service.Post("run-legacy", "Ответ."); !errors.Is(err, ErrSessionInterrupted) {
		t.Fatalf("Post = %v, want ErrSessionInterrupted", err)
	}
	// History stays available for the UI.
	messages, err := service.Messages("run-legacy")
	if err != nil || len(messages) != 1 {
		t.Fatalf("Messages = %d, %v; want the stored greeting", len(messages), err)
	}
	service.RecoverSessions(context.Background())
	select {
	case status := <-finished:
		if status != domain.RunFailed {
			t.Fatalf("legacy run status = %q, want failed", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("legacy chat run was not marked failed")
	}
}

// TestUnsupportedSessionSchemaIsHonestFailure ensures an unknown snapshot schema
// is reported instead of being resumed with the wrong cursor.
func TestUnsupportedSessionSchemaIsHonestFailure(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-schema", domain.RunRunning)
	seedDialog(t, store, "run-schema", "Аналитик данных", 1, 0, false, SessionActive)
	store.mu.Lock()
	snapshot := store.sessions["run-schema"]
	snapshot.SchemaVersion = SessionSchemaVersion + 99
	store.sessions["run-schema"] = snapshot
	store.mu.Unlock()

	finished := make(chan domain.RunStatus, 1)
	service := NewService(fakeInterviewer{}, func(_ string, status domain.RunStatus) {
		finished <- status
	})
	service.SetSessionStore(store)
	if _, err := service.Post("run-schema", "Ответ."); !errors.Is(err, ErrSessionInterrupted) {
		t.Fatalf("Post = %v, want ErrSessionInterrupted", err)
	}
	select {
	case status := <-finished:
		if status != domain.RunFailed {
			t.Fatalf("run status = %q, want failed", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run with unsupported schema was not failed")
	}
	if snapshot, _ := store.snapshotOf("run-schema"); snapshot.Status != SessionFailed {
		t.Fatalf("session status = %q, want failed", snapshot.Status)
	}
}

// TestVersionConflictDoesNotAcknowledgeAnswer covers a stale writer: another
// process advanced the durable session, so this answer must be rejected and not
// kept in memory.
func TestVersionConflictDoesNotAcknowledgeAnswer(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-conflict", domain.RunRunning)
	service := NewService(fakeInterviewer{}, nil)
	service.SetSessionStore(store)
	if err := service.Start("run-conflict", "Аналитик данных"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForStoredMessages(t, service, "run-conflict", 2)

	store.bumpVersion("run-conflict")
	before := store.messageCount("run-conflict")
	_, err := service.Post("run-conflict", "Конкретный ответ про аналитику данных.")
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Post = %v, want ErrVersionConflict", err)
	}
	if after := store.messageCount("run-conflict"); after != before {
		t.Fatalf("conflicting answer was persisted: %d -> %d", before, after)
	}
	if messages, err := service.Messages("run-conflict"); err != nil || len(messages) != before {
		t.Fatalf("conflicting answer stayed in memory: %d messages, %v", len(messages), err)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
