package chat

import (
	"context"
	"errors"
	"sync"
	"testing"

	"activity-descriptor/internal/domain"
)

// recordingFinalizer captures the session the retry hands to the post-interview
// pipeline and returns a configurable status/error.
type recordingFinalizer struct {
	mu         sync.Mutex
	calls      int
	runID      string
	profession string
	session    domain.InterviewSession
	status     domain.RunStatus
	err        error
	// entered and release let a test keep the finalizer running while it checks
	// the in-process retry guard.
	entered chan struct{}
	release chan struct{}
	// onCall runs inside the finalizer, so a test can inspect the durable state
	// while the pipeline is working.
	onCall func()
}

func (f *recordingFinalizer) finalize(_ context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error) {
	f.mu.Lock()
	f.calls++
	f.runID = runID
	f.profession = profession
	f.session = session
	status, err := f.status, f.err
	entered, release, onCall := f.entered, f.release, f.onCall
	f.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return domain.RunFailed, err
	}
	if status == "" {
		status = domain.RunDone
	}
	return status, nil
}

func (f *recordingFinalizer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *recordingFinalizer) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// statusRecorder collects the run status transitions reported through onFinish
// and mirrors them into the fake store, like the production callback does.
type statusRecorder struct {
	mu       sync.Mutex
	statuses []domain.RunStatus
}

func (r *statusRecorder) handler(store *fakeSessionStore) func(string, domain.RunStatus) {
	return func(runID string, status domain.RunStatus) {
		r.mu.Lock()
		r.statuses = append(r.statuses, status)
		r.mu.Unlock()
		store.seedRun(runID, status)
	}
}

func (r *statusRecorder) all() []domain.RunStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.RunStatus, len(r.statuses))
	copy(out, r.statuses)
	return out
}

// seedRetrySession stores a durable session and its dialog without building a
// real interviewer, so edge cases (empty history, still active interview) are
// cheap to set up.
func seedRetrySession(store *fakeSessionStore, runID, profession string, status SessionStatus, finalStatus domain.RunStatus, messages ...Message) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.sessions[runID] = SessionSnapshot{
		SchemaVersion: SessionSchemaVersion,
		Profession:    profession,
		Status:        status,
		FinalStatus:   finalStatus,
		Version:       1,
	}
	store.messages[runID] = messages
}

func (store *fakeSessionStore) sessionOf(t *testing.T, runID string) SessionSnapshot {
	t.Helper()
	snapshot, ok := store.snapshotOf(runID)
	if !ok {
		t.Fatalf("run %s has no durable session", runID)
	}
	return snapshot
}

// dialogPairs extracts the expected question/answer texts from a persisted
// dialog the same way the respondent reads them: an agent message answered by
// the following user message is one pair.
func dialogPairs(messages []Message) (questions, answers []string) {
	var pending string
	for _, message := range messages {
		switch message.Role {
		case "agent":
			pending = message.Content
		case "user":
			if pending == "" {
				continue
			}
			questions = append(questions, pending)
			answers = append(answers, message.Content)
			pending = ""
		}
	}
	return questions, answers
}

func newRetryService(store *fakeSessionStore, finalizer Finalizer, recorder *statusRecorder) *Service {
	service := NewService(nil, recorder.handler(store))
	service.SetSessionStore(store)
	if finalizer != nil {
		service.SetFinalizer(finalizer)
	}
	return service
}

func TestRetryFinalizeAnalysesPersistedTranscript(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-retry", domain.RunFailed)
	seedDialog(t, store, "run-retry", "Аналитик данных", 3, 3, true, SessionFailed)

	persisted, err := store.ListMessages(context.Background(), "run-retry")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	wantQuestions, wantAnswers := dialogPairs(persisted)
	if len(wantQuestions) != 3 || len(wantAnswers) != 3 {
		t.Fatalf("seed dialog has %d questions / %d answers, want 3/3", len(wantQuestions), len(wantAnswers))
	}

	finalizer := &recordingFinalizer{}
	recorder := &statusRecorder{}
	service := newRetryService(store, finalizer.finalize, recorder)

	// Pretend the failed run still lives in memory (complete keeps the finished
	// session cached); the retry must drop it instead of reusing it.
	service.mu.Lock()
	service.sessions["run-retry"] = &session{
		runID:       "run-retry",
		profession:  "Аналитик данных",
		subscribers: make(map[chan Event]struct{}),
	}
	service.mu.Unlock()

	// The precondition check is read-only: it must not reset the session or
	// start the pipeline before the caller decides to retry.
	if err := service.CheckRetryFinalize(context.Background(), "run-retry"); err != nil {
		t.Fatalf("CheckRetryFinalize: %v", err)
	}
	if store.saves != 0 {
		t.Fatalf("CheckRetryFinalize wrote %d snapshots, want 0", store.saves)
	}
	if snapshot := store.sessionOf(t, "run-retry"); snapshot.Status != SessionFailed {
		t.Fatalf("CheckRetryFinalize changed session status to %q, want failed", snapshot.Status)
	}

	var duringRetry SessionSnapshot
	finalizer.onCall = func() { duringRetry = store.sessionOf(t, "run-retry") }

	if err := service.RetryFinalize(context.Background(), "run-retry"); err != nil {
		t.Fatalf("RetryFinalize: %v", err)
	}
	if duringRetry.Status != SessionActive {
		t.Fatalf("durable session status during the retry = %q, want active", duringRetry.Status)
	}
	service.mu.Lock()
	_, cached := service.sessions["run-retry"]
	service.mu.Unlock()
	if cached {
		t.Fatal("in-memory session was not forgotten by the retry")
	}
	if got := finalizer.callCount(); got != 1 {
		t.Fatalf("finalizer calls = %d, want 1", got)
	}
	if finalizer.runID != "run-retry" || finalizer.profession != "Аналитик данных" {
		t.Fatalf("finalizer got run %q profession %q", finalizer.runID, finalizer.profession)
	}
	if finalizer.session.ID != "run-run-retry" {
		t.Fatalf("session id = %q, want run-run-retry", finalizer.session.ID)
	}
	if len(finalizer.session.Questions) != len(wantQuestions) || len(finalizer.session.Answers) != len(wantAnswers) {
		t.Fatalf("session has %d questions / %d answers, want %d/%d",
			len(finalizer.session.Questions), len(finalizer.session.Answers), len(wantQuestions), len(wantAnswers))
	}
	for index := range wantQuestions {
		if finalizer.session.Questions[index].Text != wantQuestions[index] {
			t.Fatalf("question %d = %q, want %q", index, finalizer.session.Questions[index].Text, wantQuestions[index])
		}
		if finalizer.session.Answers[index].Text != wantAnswers[index] {
			t.Fatalf("answer %d = %q, want %q", index, finalizer.session.Answers[index].Text, wantAnswers[index])
		}
		if finalizer.session.Answers[index].QuestionID != finalizer.session.Questions[index].ID {
			t.Fatalf("answer %d points at question %q, want %q",
				index, finalizer.session.Answers[index].QuestionID, finalizer.session.Questions[index].ID)
		}
	}

	// The durable session was reset to active before the pipeline ran and then
	// written back as completed/done.
	if store.saves != 2 {
		t.Fatalf("SaveStep calls = %d, want 2 (reset + result)", store.saves)
	}
	snapshot := store.sessionOf(t, "run-retry")
	if snapshot.Status != SessionCompleted || snapshot.FinalStatus != domain.RunDone {
		t.Fatalf("session after retry = %q/%q, want completed/done", snapshot.Status, snapshot.FinalStatus)
	}
	if got := recorder.all(); len(got) != 1 || got[0] != domain.RunDone {
		t.Fatalf("run status transitions = %v, want [done]", got)
	}
}

func TestRetryFinalizeFailureStaysRetryable(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-failed", domain.RunFailed)
	seedDialog(t, store, "run-failed", "Аналитик данных", 2, 2, true, SessionFailed)

	finalizer := &recordingFinalizer{err: errors.New("llm: provider is not configured")}
	recorder := &statusRecorder{}
	service := newRetryService(store, finalizer.finalize, recorder)

	err := service.RetryFinalize(context.Background(), "run-failed")
	if !errors.Is(err, finalizer.err) {
		t.Fatalf("first RetryFinalize error = %v, want %v", err, finalizer.err)
	}
	if got := finalizer.callCount(); got != 1 {
		t.Fatalf("finalizer calls = %d, want 1", got)
	}
	snapshot := store.sessionOf(t, "run-failed")
	if snapshot.Status != SessionFailed || snapshot.FinalStatus != domain.RunFailed {
		t.Fatalf("session after failed retry = %q/%q, want failed/failed", snapshot.Status, snapshot.FinalStatus)
	}
	if got := recorder.all(); len(got) != 1 || got[0] != domain.RunFailed {
		t.Fatalf("run status transitions = %v, want [failed]", got)
	}

	// No terminal flag is written: the same run can be retried again and the
	// finalizer is reached with the same persisted transcript.
	finalizer.setErr(nil)
	if err := service.RetryFinalize(context.Background(), "run-failed"); err != nil {
		t.Fatalf("second RetryFinalize: %v", err)
	}
	if got := finalizer.callCount(); got != 2 {
		t.Fatalf("finalizer calls after second retry = %d, want 2", got)
	}
	snapshot = store.sessionOf(t, "run-failed")
	if snapshot.Status != SessionCompleted || snapshot.FinalStatus != domain.RunDone {
		t.Fatalf("session after successful retry = %q/%q, want completed/done", snapshot.Status, snapshot.FinalStatus)
	}
}

func TestRetryFinalizeRejectsExistingTree(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-tree", domain.RunFailed)
	seedDialog(t, store, "run-tree", "Аналитик данных", 1, 1, true, SessionFailed)
	store.trees["run-tree"] = true

	finalizer := &recordingFinalizer{}
	service := newRetryService(store, finalizer.finalize, &statusRecorder{})

	if err := service.CheckRetryFinalize(context.Background(), "run-tree"); !errors.Is(err, ErrTreeAlreadyExists) {
		t.Fatalf("CheckRetryFinalize = %v, want ErrTreeAlreadyExists", err)
	}
	if err := service.RetryFinalize(context.Background(), "run-tree"); !errors.Is(err, ErrTreeAlreadyExists) {
		t.Fatalf("RetryFinalize = %v, want ErrTreeAlreadyExists", err)
	}
	if got := finalizer.callCount(); got != 0 {
		t.Fatalf("finalizer calls = %d, want 0", got)
	}
	if store.saves != 0 {
		t.Fatalf("SaveStep calls = %d, want 0", store.saves)
	}
	if snapshot := store.sessionOf(t, "run-tree"); snapshot.Status != SessionFailed {
		t.Fatalf("session status = %q, want failed (untouched)", snapshot.Status)
	}
}

func TestRetryFinalizeRequiresMessages(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-empty", domain.RunFailed)
	seedRetrySession(store, "run-empty", "Аналитик данных", SessionFailed, domain.RunFailed)

	finalizer := &recordingFinalizer{}
	service := newRetryService(store, finalizer.finalize, &statusRecorder{})

	if err := service.RetryFinalize(context.Background(), "run-empty"); !errors.Is(err, ErrNothingToRetry) {
		t.Fatalf("RetryFinalize = %v, want ErrNothingToRetry", err)
	}
	if got := finalizer.callCount(); got != 0 {
		t.Fatalf("finalizer calls = %d, want 0", got)
	}
}

func TestRetryFinalizeRejectsActiveInterview(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-active", domain.RunRunning)
	seedRetrySession(store, "run-active", "Аналитик данных", SessionActive, "",
		newMessage("run-active", 1, "agent", welcomeMessage))

	finalizer := &recordingFinalizer{}
	service := newRetryService(store, finalizer.finalize, &statusRecorder{})

	if err := service.RetryFinalize(context.Background(), "run-active"); !errors.Is(err, ErrRetryInProgress) {
		t.Fatalf("RetryFinalize = %v, want ErrRetryInProgress", err)
	}
	if got := finalizer.callCount(); got != 0 {
		t.Fatalf("finalizer calls = %d, want 0", got)
	}
}

func TestRetryFinalizeWithoutFinalizer(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-nofin", domain.RunFailed)
	seedDialog(t, store, "run-nofin", "Аналитик данных", 1, 1, true, SessionFailed)

	service := NewService(nil, nil)
	service.SetSessionStore(store)

	if err := service.RetryFinalize(context.Background(), "run-nofin"); !errors.Is(err, ErrFinalizerUnavailable) {
		t.Fatalf("RetryFinalize = %v, want ErrFinalizerUnavailable", err)
	}
	if store.saves != 0 {
		t.Fatalf("SaveStep calls = %d, want 0", store.saves)
	}
	if snapshot := store.sessionOf(t, "run-nofin"); snapshot.Status != SessionFailed {
		t.Fatalf("session status = %q, want failed (untouched)", snapshot.Status)
	}
}

func TestRetryFinalizeUnknownRun(t *testing.T) {
	store := newFakeSessionStore()
	finalizer := &recordingFinalizer{}
	service := newRetryService(store, finalizer.finalize, &statusRecorder{})

	if err := service.RetryFinalize(context.Background(), "run-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RetryFinalize = %v, want ErrNotFound", err)
	}
	if got := finalizer.callCount(); got != 0 {
		t.Fatalf("finalizer calls = %d, want 0", got)
	}
}

func TestRetryFinalizeRejectsConcurrentRetry(t *testing.T) {
	store := newFakeSessionStore()
	store.seedRun("run-double", domain.RunFailed)
	seedDialog(t, store, "run-double", "Аналитик данных", 1, 1, true, SessionFailed)

	finalizer := &recordingFinalizer{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	service := newRetryService(store, finalizer.finalize, &statusRecorder{})

	done := make(chan error, 1)
	go func() { done <- service.RetryFinalize(context.Background(), "run-double") }()
	<-finalizer.entered

	if err := service.RetryFinalize(context.Background(), "run-double"); !errors.Is(err, ErrRetryInProgress) {
		t.Fatalf("concurrent RetryFinalize = %v, want ErrRetryInProgress", err)
	}
	close(finalizer.release)
	if err := <-done; err != nil {
		t.Fatalf("first RetryFinalize: %v", err)
	}
}

// TestSessionFromMessagesSkipsServiceMessages documents how the persisted
// dialog becomes the analyst transcript: the greeting and the closing status
// message are not questions, and an unanswered question is not evidence.
func TestSessionFromMessagesSkipsServiceMessages(t *testing.T) {
	messages := []Message{
		newMessage("run-x", 1, "agent", welcomeMessage),
		newMessage("run-x", 2, "agent", "Какую роль вы выполняете?"),
		newMessage("run-x", 3, "user", "Я аналитик данных."),
		newMessage("run-x", 4, "agent", "Какие задачи вы решаете?"),
		newMessage("run-x", 5, "user", "Готовлю отчётность."),
		newMessage("run-x", 6, "agent", "Интервью завершено. Результат исследования доступен во вкладке «Дерево»."),
	}
	session := sessionFromMessages("run-x", "Аналитик данных", messages)
	if len(session.Questions) != 2 || len(session.Answers) != 2 {
		t.Fatalf("session has %d questions / %d answers, want 2/2", len(session.Questions), len(session.Answers))
	}
	if session.Questions[0].Text != "Какую роль вы выполняете?" || session.Answers[0].Text != "Я аналитик данных." {
		t.Fatalf("first pair = %q / %q", session.Questions[0].Text, session.Answers[0].Text)
	}
	if session.Questions[1].Text != "Какие задачи вы решаете?" || session.Answers[1].Text != "Готовлю отчётность." {
		t.Fatalf("second pair = %q / %q", session.Questions[1].Text, session.Answers[1].Text)
	}
	if session.ID != "run-run-x" || session.Profession != "Аналитик данных" {
		t.Fatalf("session identity = %q/%q", session.ID, session.Profession)
	}
}

// TestSessionFromMessagesIgnoresStrayAnswer covers a dialog that starts with a
// user message: it cannot be paired with a question and must not shift the
// following pairs.
func TestSessionFromMessagesIgnoresStrayAnswer(t *testing.T) {
	messages := []Message{
		newMessage("run-y", 1, "user", "Ответ без вопроса."),
		newMessage("run-y", 2, "agent", "Первый вопрос?"),
		newMessage("run-y", 3, "user", "Первый ответ."),
	}
	session := sessionFromMessages("run-y", "", messages)
	if len(session.Questions) != 1 || len(session.Answers) != 1 {
		t.Fatalf("session has %d questions / %d answers, want 1/1", len(session.Questions), len(session.Answers))
	}
	if session.Questions[0].Text != "Первый вопрос?" || session.Answers[0].Text != "Первый ответ." {
		t.Fatalf("pair = %q / %q", session.Questions[0].Text, session.Answers[0].Text)
	}
}
