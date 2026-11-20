// Package chat keeps the interviewer dialog for one run, mirrors it to durable
// storage when configured, and exposes messages and events to the HTTP API.
//
// The active dialog (SSE events, in-flight interviewer questions) stays in
// memory, but the dialog messages and a snapshot of the interviewer session are
// written through SessionStore in the same transaction. A restarted backend
// therefore reloads the session lazily from PostgreSQL and continues the
// interview at the exact question/answer cursor instead of leaving an active
// run hanging.
package chat

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llmobs"
)

// Citation is an optional reference attached to an agent message. The field is
// part of the public chat contract; the interviewer does not produce citations
// yet, so it is normally absent.
type Citation struct {
	Title string `json:"title"`
	Quote string `json:"quote"`
}

// Message is one chat message. Role is user or agent.
type Message struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	Citations []Citation `json:"citations,omitempty"`
	// Sequence is the durable 1-based position inside the run dialog. It is
	// not part of the HTTP representation: the public ID already embeds it.
	Sequence int `json:"-"`
}

// MessageStore persists the dialog of one run outside the process. The chat
// service writes every message through it and reads the history back when the
// in-memory session no longer exists (completed run or backend restart).
//
// Implementations must be idempotent for the same (run_id, sequence) pair:
// a retried AppendMessage for an already stored position must succeed without
// creating a duplicate row. ListMessages returns ErrNotFound when the run
// itself does not exist. Messages must be ordered by sequence ascending.
type MessageStore interface {
	AppendMessage(ctx context.Context, runID string, message Message) error
	ListMessages(ctx context.Context, runID string) ([]Message, error)
}

// Event is one server-sent chat event.
type Event struct {
	Type    string   `json:"type"`
	Message *Message `json:"message,omitempty"`
	Status  string   `json:"status,omitempty"`
}

// ErrNotFound means the run has no chat session.
var ErrNotFound = errors.New("chat: session not found")

// ErrEmptyMessage means the user tried to send an empty message.
var ErrEmptyMessage = errors.New("chat: message is empty")

var ErrInvalidMessage = errors.New("chat: message cannot be stored as PostgreSQL text")

// UnavailableClient is used when no LLM provider is configured. The interviewer
// session falls back to deterministic questions instead of failing the chat.
type UnavailableClient struct{}

// Chat implements llm.ChatClient.
func (UnavailableClient) Chat(context.Context, []llm.Message) (string, error) {
	return "", errors.New("llm: provider is not configured")
}

// Finalizer turns a finished interview into the final tree. It is called once
// after all questions are answered and before the run is marked done.
type Finalizer func(ctx context.Context, runID, profession string, session domain.InterviewSession) (domain.RunStatus, error)

var ErrAnalysisUnavailable = errors.New("chat: AI analysis unavailable")

// SourceContextLoader builds a compact summary of vacancies and materials
// relevant to the profession. It is best-effort: an error or empty string just
// leaves the interviewer without extra context.
type SourceContextLoader func(runID, profession string) string

const welcomeMessage = "Здравствуйте! Я чат-интервьюер. Я задам вам несколько вопросов о вашей профессиональной деятельности, чтобы описать ваши функции, задачи и действия."
const learningWelcomeMessage = "Здравствуйте! Я помогу составить план обучения. Сначала задам несколько вопросов о вашем текущем опыте и знаниях, чтобы не повторять то, что вы уже умеете."

// resumeMode records what the dialog still owes when a session was restored
// from the database: either the next question or the post-interview pipeline.
type resumeMode int

const (
	resumeNone resumeMode = iota
	resumeAsk
	resumeComplete
)

// Service owns one interviewer session per run.
type Service struct {
	client         llm.ChatClient
	finalizer      Finalizer
	onFinish       func(runID string, status domain.RunStatus)
	promptVersion  string
	promptVersionB string
	sourceContext  SourceContextLoader

	mu            sync.Mutex
	sessions      map[string]*session
	profileData   map[string]string
	profileSkills map[string][]string
	store         MessageStore
	sessionStore  SessionStore
	// startedAt is the instant this service was created. Startup recovery only
	// considers runs and sessions that predate it.
	startedAt time.Time
	// restoreMu serializes lazy restores so two requests for the same run never
	// build two in-memory sessions; the map check under mu is repeated inside.
	restoreMu sync.Mutex
}

type session struct {
	runID         string
	profession    string
	promptVersion string

	mu          sync.Mutex
	session     *interviewer.Session
	messages    []Message
	seq         int
	version     int64
	asking      bool
	finalizing  bool
	finished    bool
	learning    bool
	resume      resumeMode
	subscribers map[chan Event]struct{}
}

// NewService creates a chat service. client may be nil, in which case fallback
// questions are used. onFinish is optional.
func NewService(client llm.ChatClient, onFinish func(string, domain.RunStatus)) *Service {
	if client == nil {
		client = UnavailableClient{}
	}
	return &Service{
		client:        client,
		onFinish:      onFinish,
		sessions:      make(map[string]*session),
		profileData:   make(map[string]string),
		profileSkills: make(map[string][]string),
		startedAt:     time.Now().UTC(),
	}
}

// SetMessageStore installs the durable history store. It must be called before
// the first run is started. Nil (the default) keeps the dialog in memory only,
// which is the demo/test setup.
//
// SetSessionStore is preferred in production: it persists the interviewer
// session snapshot as well, which is what makes a restart resumable.
func (s *Service) SetMessageStore(store MessageStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}

// SetSessionStore installs the durable session store. It also serves as the
// message store, so the history survives a restart and active interviews can be
// resumed. It must be called before the first run is started.
func (s *Service) SetSessionStore(store SessionStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionStore = store
	if store != nil {
		s.store = store
	}
}

// messageStore returns the configured persistence layer. It is read on every
// write so SetMessageStore is safe with a concurrently running service.
func (s *Service) messageStore() MessageStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store
}

// sessionStoreOf returns the configured durable session store, if any.
func (s *Service) sessionStoreOf() SessionStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionStore
}

// SetFinalizer installs the post-interview pipeline. It must be called before
// the first run is started.
func (s *Service) SetFinalizer(finalizer Finalizer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizer = finalizer
}

// finalizerFn returns the configured finalizer safely.
func (s *Service) finalizerFn() Finalizer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalizer
}

// SetPromptVersion pins the interviewer prompt version for new sessions.
func (s *Service) SetPromptVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptVersion = version
}

// SetPromptVersionB enables a deterministic A/B split: even run ids use the
// primary prompt version, odd run ids use version B. Empty B keeps one version.
func (s *Service) SetPromptVersionB(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptVersionB = version
}

func (s *Service) promptVersionFor(runID string) string {
	s.mu.Lock()
	primary, variant := s.promptVersion, s.promptVersionB
	s.mu.Unlock()
	if variant == "" || len(runID) == 0 {
		return primary
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(runID))
	if hash.Sum32()%2 == 0 {
		return primary
	}
	return variant
}

// SetSourceContextLoader installs a best-effort provider of vacancy/material
// context for new and restored interviewer sessions.
func (s *Service) SetSourceContextLoader(loader SourceContextLoader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sourceContext = loader
}

func (s *Service) sourceContextFor(runID, profession string) string {
	s.mu.Lock()
	loader := s.sourceContext
	s.mu.Unlock()
	if loader == nil {
		return ""
	}
	return loader(runID, profession)
}

// SetProfileContext attaches public GitHub data to the next interview started
// for runID. The API calls it before Start or StartLearning; the context is
// consumed once and then persisted inside the interviewer snapshot.
func (s *Service) SetProfileContext(runID, context string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profileData == nil {
		s.profileData = make(map[string]string)
	}
	s.profileData[runID] = strings.TrimSpace(context)
}

func (s *Service) takeProfileContext(runID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	context := s.profileData[runID]
	delete(s.profileData, runID)
	return context
}

// SetProfileSkills attaches the skills found in the public GitHub profile.
func (s *Service) SetProfileSkills(runID string, skills []string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profileSkills == nil {
		s.profileSkills = make(map[string][]string)
	}
	s.profileSkills[runID] = append([]string(nil), skills...)
}

func (s *Service) takeProfileSkills(runID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	skills := s.profileSkills[runID]
	delete(s.profileSkills, runID)
	return skills
}

// newAgent builds the interviewer agent for one session.
func (s *Service) newAgent(version string) (*interviewer.Agent, error) {
	opts := []interviewer.Option{}
	if version != "" {
		opts = append(opts, interviewer.WithPromptVersion(version))
	}
	agent, err := interviewer.New(s.client, opts...)
	if err != nil {
		return nil, fmt.Errorf("chat: interviewer: %w", err)
	}
	return agent, nil
}

// Start creates the interview session and asks the first question.
// Start creates the interview session and asks the first question.
func (s *Service) Start(runID, profession string) error {
	return s.start(runID, profession, false)
}

// StartLearning starts the beginner-oriented interview and asks about the
// skills the person already has before the learning plan is generated.
func (s *Service) StartLearning(runID, profession string) error {
	return s.start(runID, profession, true)
}

func (s *Service) start(runID, profession string, learning bool) error {
	if runID == "" {
		return fmt.Errorf("chat: run id is required")
	}
	promptVersion := s.promptVersionFor(runID)
	agent, err := s.newAgent(promptVersion)
	if err != nil {
		return err
	}
	interview, err := interviewer.NewSession(agent, "run-"+runID, domain.Respondent{
		ID:         "respondent-" + runID,
		Role:       profession,
		Profession: profession,
	})
	if err != nil {
		return fmt.Errorf("chat: session: %w", err)
	}
	interview.SetLearning(learning)
	interview.SetSourceContext(s.sourceContextFor(runID, profession))
	interview.SetProfileContext(s.takeProfileContext(runID))
	interview.SetProfileSkills(s.takeProfileSkills(runID))

	s.mu.Lock()
	if _, exists := s.sessions[runID]; exists {
		s.mu.Unlock()
		return nil
	}
	sess := &session{
		runID:         runID,
		profession:    profession,
		promptVersion: promptVersion,
		session:       interview,
		learning:      learning,
		subscribers:   make(map[chan Event]struct{}),
	}
	s.sessions[runID] = sess
	s.mu.Unlock()

	// The greeting is persisted together with the initial snapshot: after a
	// restart the dialog history and the session cursor are never out of sync.
	sess.mu.Lock()
	greeting := welcomeMessage
	if learning {
		greeting = learningWelcomeMessage
	}
	message := newMessage(runID, 1, "agent", greeting)
	if err := s.persistStep(sess, s.snapshotLocked(sess, SessionActive, ""), []Message{message}); err != nil {
		sess.mu.Unlock()
		s.mu.Lock()
		delete(s.sessions, runID)
		s.mu.Unlock()
		return fmt.Errorf("chat: persist greeting: %w", err)
	}
	sess.messages = append(sess.messages, message)
	sess.seq = message.Sequence
	sess.asking = true
	sess.mu.Unlock()
	s.publish(runID, Event{Type: "agent_message", Message: &message})
	s.publish(runID, Event{Type: "agent_thinking"})
	go s.askNext(sess)
	return nil
}

// Progress returns live interview coverage and answer quality.
func (s *Service) Progress(runID string) (interviewer.Progress, error) {
	sess, err := s.lookup(runID)
	if err != nil {
		return interviewer.Progress{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.session.Progress(), nil
}

// Messages returns a copy of the chat history. When the active session is not
// in memory (a completed run, or a run served by a restarted backend) the
// history is loaded from the durable store. ErrNotFound means the run has no
// chat history and no active session.
func (s *Service) Messages(runID string) ([]Message, error) {
	if runID == "" {
		return nil, ErrNotFound
	}
	sess, err := s.lookup(runID)
	if err == nil {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		out := make([]Message, len(sess.messages))
		copy(out, sess.messages)
		return out, nil
	}
	// Finished and interrupted runs are read-only: their persisted history is
	// still served instead of returning an error to the UI.
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrSessionFinished) || errors.Is(err, ErrSessionInterrupted) {
		return s.persistedMessages(runID)
	}
	return nil, err
}

// persistedMessages reads the history of a run that has no in-memory session.
func (s *Service) persistedMessages(runID string) ([]Message, error) {
	store := s.messageStore()
	if store == nil {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	messages, err := store.ListMessages(ctx, runID)
	if err != nil {
		return nil, err
	}
	if messages == nil {
		messages = []Message{}
	}
	return messages, nil
}

// Post records the respondent answer and schedules the next question.
func (s *Service) Post(runID, content string) (Message, error) {
	if strings.TrimSpace(content) == "" {
		return Message{}, ErrEmptyMessage
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return Message{}, ErrInvalidMessage
	}
	sess, err := s.lookup(runID)
	if err != nil {
		return Message{}, err
	}

	sess.mu.Lock()
	if sess.finished {
		sess.mu.Unlock()
		return Message{}, ErrSessionFinished
	}
	if sess.finalizing {
		// Every answer is already collected; the pipeline is running.
		sess.mu.Unlock()
		return Message{}, ErrSessionFinished
	}
	if sess.asking {
		// The interviewer is generating the question. The interviewer state is
		// only mutated while asking is true, so answering now would race with it.
		sess.mu.Unlock()
		return Message{}, ErrQuestionPending
	}
	if !sess.session.PendingAnswer() {
		// There is no open question: the interviewer may still be preparing it,
		// or a restored session still owes the next question. Resume exactly once
		// and tell the client to retry instead of accepting a second answer.
		pending := sess.resume
		sess.mu.Unlock()
		switch pending {
		case resumeAsk:
			s.resume(sess)
			return Message{}, ErrQuestionPending
		case resumeComplete:
			s.resume(sess)
			return Message{}, ErrSessionFinished
		}
		return Message{}, ErrQuestionPending
	}

	// Capture the previous state so a failed durable write does not acknowledge
	// an answer the client cannot later rely on.
	snapshotBefore := sess.session.Snapshot()
	message := newMessage(runID, sess.seq+1, "user", content)
	if err := sess.session.Answer(content); err != nil {
		sess.mu.Unlock()
		return Message{}, fmt.Errorf("chat: answer: %w", err)
	}
	done := sess.session.Done()
	status := SessionActive
	if done {
		// Close before the snapshot so the persisted session has the final
		// interviewer status; the pipeline itself runs asynchronously.
		sess.session.Close()
		status = SessionFinalizing
	}
	if err := s.persistStep(sess, s.snapshotLocked(sess, status, ""), []Message{message}); err != nil {
		if restoreErr := sess.session.Restore(snapshotBefore); restoreErr != nil {
			log.Printf("chat: rollback session state for run %s: %v", runID, restoreErr)
		}
		if errors.Is(err, ErrVersionConflict) {
			// Another process advanced the durable session; reload it on the next
			// request instead of serving a permanently stale in-memory cursor.
			s.forgetSession(runID)
		}
		sess.mu.Unlock()
		return Message{}, fmt.Errorf("chat: persist answer: %w", err)
	}
	sess.messages = append(sess.messages, message)
	sess.seq = message.Sequence

	if done {
		sess.finalizing = true
		sess.mu.Unlock()
		s.publish(runID, Event{Type: "agent_thinking"})
		go s.complete(sess)
		return message, nil
	}

	sess.asking = true
	sess.mu.Unlock()
	s.publish(runID, Event{Type: "agent_thinking"})
	go s.askNext(sess)
	return message, nil
}

// Subscribe returns a channel of events and a cancel function.
func (s *Service) Subscribe(runID string) (<-chan Event, func(), error) {
	sess, err := s.lookup(runID)
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan Event, 16)
	sess.mu.Lock()
	sess.subscribers[ch] = struct{}{}
	sess.mu.Unlock()
	cancel := func() {
		sess.mu.Lock()
		if _, ok := sess.subscribers[ch]; ok {
			delete(sess.subscribers, ch)
			close(ch)
		}
		sess.mu.Unlock()
	}
	// A restored session may still owe the respondent the next question or the
	// completed run. Resume only after the subscriber is registered, so the
	// generated event is delivered instead of being published into the void.
	s.resume(sess)
	return ch, cancel, nil
}

func (s *Service) lookup(runID string) (*session, error) {
	if runID == "" {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	sess, ok := s.sessions[runID]
	s.mu.Unlock()
	if ok {
		return sess, nil
	}
	store := s.sessionStoreOf()
	if store == nil {
		// No durable sessions: behaviour is unchanged from the memory-only setup.
		return nil, ErrNotFound
	}
	return s.restore(runID, store)
}

// restore rebuilds an in-memory session from the durable snapshot. The caller
// holds no locks; restoreMu serializes concurrent restores of the same run.
func (s *Service) restore(runID string, store SessionStore) (*session, error) {
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()

	s.mu.Lock()
	if sess, ok := s.sessions[runID]; ok {
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	snapshot, err := store.LoadSession(ctx, runID)
	if errors.Is(err, ErrNotFound) {
		return nil, s.restoreLegacyRun(ctx, store, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("chat: load session: %w", err)
	}
	switch snapshot.Status {
	case SessionCompleted, SessionFailed:
		s.reconcileFinishedRun(store, runID, snapshot)
		return nil, ErrSessionFinished
	case SessionActive, SessionFinalizing:
		// Active sessions are restorable; handled below.
	default:
		log.Printf("chat: run %s has unknown chat session status %q; SESSION_INTERRUPTED", runID, snapshot.Status)
		s.markSessionFailed(runID, snapshot)
		s.notifyFinish(runID, domain.RunFailed)
		return nil, ErrSessionInterrupted
	}
	if snapshot.SchemaVersion != SessionSchemaVersion {
		log.Printf("chat: run %s has unsupported chat session schema %d (want %d); SESSION_INTERRUPTED",
			runID, snapshot.SchemaVersion, SessionSchemaVersion)
		s.markSessionFailed(runID, snapshot)
		s.notifyFinish(runID, domain.RunFailed)
		return nil, ErrSessionInterrupted
	}
	runStatus, err := store.RunStatus(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("chat: load run status: %w", err)
	}
	if isTerminalRunStatus(runStatus) {
		// The run is over; the history stays readable but there is nothing to
		// resume.
		s.reconcileFinishedRun(store, runID, snapshot)
		return nil, ErrSessionFinished
	}

	agent, err := s.newAgent(snapshot.PromptVersion)
	if err != nil {
		log.Printf("chat: restore interviewer for run %s: %v; SESSION_INTERRUPTED", runID, err)
		s.markSessionFailed(runID, snapshot)
		s.notifyFinish(runID, domain.RunFailed)
		return nil, ErrSessionInterrupted
	}
	interview, err := interviewer.RestoreSession(agent, snapshot.Interviewer)
	if err != nil {
		log.Printf("chat: restore session for run %s: %v; SESSION_INTERRUPTED", runID, err)
		s.markSessionFailed(runID, snapshot)
		s.notifyFinish(runID, domain.RunFailed)
		return nil, ErrSessionInterrupted
	}
	interview.SetSourceContext(s.sourceContextFor(runID, snapshot.Profession))
	messages, err := store.ListMessages(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("chat: load messages: %w", err)
	}
	// Older snapshots only identify learning interviews by their persisted greeting.
	learning := snapshot.Learning || (len(messages) > 0 && messages[0].Role == "agent" && messages[0].Content == learningWelcomeMessage)
	interview.SetLearning(learning)
	sess := &session{
		learning:      learning,
		runID:         runID,
		profession:    snapshot.Profession,
		promptVersion: snapshot.PromptVersion,
		session:       interview,
		messages:      messages,
		seq:           lastSequence(messages),
		version:       snapshot.Version,
		subscribers:   make(map[chan Event]struct{}),
	}
	switch {
	case snapshot.Status == SessionFinalizing:
		// The pipeline was interrupted after the last answer: finish it.
		interview.Close()
		sess.resume = resumeComplete
	case interview.Done():
		// Compatibility with snapshots written before the finalizing status.
		interview.Close()
		sess.resume = resumeComplete
	case interview.PendingAnswer():
		sess.resume = resumeNone
	default:
		// The answer is already stored but the next question was never written
		// (the process died mid-LLM call): ask it once.
		sess.resume = resumeAsk
	}

	s.mu.Lock()
	s.sessions[runID] = sess
	s.mu.Unlock()
	return sess, nil
}

// restoreLegacyRun handles a chat run that has history but no durable session
// snapshot (created by a backend version before this feature). Its dialog
// cannot be continued: the run is reported as interrupted and the startup sweep
// marks it failed.
func (s *Service) restoreLegacyRun(ctx context.Context, store SessionStore, runID string) error {
	status, err := store.RunStatus(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("chat: load run status: %w", err)
	}
	if isTerminalRunStatus(status) {
		return ErrSessionFinished
	}
	// Batch runs have no chat session at all. Probing /messages must not turn
	// such a run into a failed chat: only a run with persisted chat history was
	// created by the legacy chat implementation.
	messages, err := store.ListMessages(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("chat: load legacy messages: %w", err)
	}
	if len(messages) == 0 {
		return ErrNotFound
	}
	log.Printf("chat: run %s has chat history but no durable session snapshot; SESSION_INTERRUPTED", runID)
	// Honest failure marking: the dialog can never continue, so the run must not
	// stay "running" while the UI waits for a question.
	s.notifyFinish(runID, domain.RunFailed)
	return ErrSessionInterrupted
}

// reconcileFinishedRun finishes the run status transition when the process died
// between writing the terminal session snapshot and calling onFinish.
func (s *Service) reconcileFinishedRun(store SessionStore, runID string, snapshot SessionSnapshot) {
	status := snapshot.FinalStatus
	if !status.Valid() {
		if snapshot.Status == SessionFailed {
			status = domain.RunFailed
		} else {
			status = domain.RunDone
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	runStatus, err := store.RunStatus(ctx, runID)
	if err != nil || isTerminalRunStatus(runStatus) {
		return
	}
	log.Printf("chat: run %s has a finalized chat session; completing status transition to %s", runID, status)
	s.notifyFinish(runID, status)
}

// markSessionFailed durably records that a session cannot be restored, so the
// next request reports SESSION_INTERRUPTED instead of retrying forever.
func (s *Service) markSessionFailed(runID string, snapshot SessionSnapshot) {
	store := s.sessionStoreOf()
	if store == nil {
		return
	}
	snapshot.Status = SessionFailed
	snapshot.FinalStatus = domain.RunFailed
	if snapshot.SchemaVersion == 0 {
		snapshot.SchemaVersion = SessionSchemaVersion
	}
	if strings.TrimSpace(snapshot.Profession) == "" {
		// The row cannot satisfy the profession NOT NULL/CHECK constraint; the
		// run is failed in memory and will be retried on the next request.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	if err := store.SaveStep(ctx, runID, snapshot, nil); err != nil {
		log.Printf("chat: mark session failed for run %s: %v", runID, err)
	}
}

// resume continues whatever the restored dialog still owes. It is safe to call
// more than once: only the first call sees a non-empty resume mode.
func (s *Service) resume(sess *session) {
	sess.mu.Lock()
	mode := sess.resume
	sess.resume = resumeNone
	if mode == resumeNone || sess.finished || sess.finalizing {
		sess.mu.Unlock()
		return
	}
	switch mode {
	case resumeAsk:
		if sess.asking || sess.session.PendingAnswer() || sess.session.Done() {
			sess.mu.Unlock()
			return
		}
		sess.asking = true
		sess.mu.Unlock()
		s.publish(sess.runID, Event{Type: "agent_thinking"})
		go s.askNext(sess)
	case resumeComplete:
		sess.finalizing = true
		sess.mu.Unlock()
		s.publish(sess.runID, Event{Type: "agent_thinking"})
		go s.complete(sess)
	default:
		sess.mu.Unlock()
	}
}

func (s *Service) askNext(sess *session) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// Name the run in the audit context, so the LLM request rows written by
	// llmobs.Recorder carry llm_requests.run_id and the run cost endpoint can
	// attribute the interviewer calls exactly.
	ctx = llmobs.WithRunID(ctx, sess.runID)

	// The model call runs without holding sess.mu: while asking is true Post
	// rejects answers instead of blocking on a mutex for the whole LLM round
	// trip. asking is cleared under sess.mu in the same critical section that
	// publishes the question, so a client never sees the question while Post
	// still reports it as pending.
	sess.mu.Lock()
	if sess.finished || sess.finalizing || sess.session.PendingAnswer() {
		sess.asking = false
		sess.mu.Unlock()
		return
	}
	sess.mu.Unlock()

	question, err := sess.session.Next(ctx)
	if err != nil {
		sess.mu.Lock()
		sess.asking = false
		sess.mu.Unlock()
		if errors.Is(err, interviewer.ErrSessionDone) {
			return
		}
		s.failSession(sess)
		return
	}

	sess.mu.Lock()
	message := newMessage(sess.runID, sess.seq+1, "agent", question.Text)
	persistErr := s.persistStep(sess, s.snapshotLocked(sess, SessionActive, ""), []Message{message})
	sess.messages = append(sess.messages, message)
	sess.seq = message.Sequence
	sess.asking = false
	versionConflict := errors.Is(persistErr, ErrVersionConflict)
	if !versionConflict {
		// Делаем вопрос доступным для ответа в той же секции, где публикуем его.
		sess.publishLocked(Event{Type: "agent_message", Message: &message})
	}
	sess.mu.Unlock()
	if persistErr != nil {
		// The dialog continues in memory; after a restart the question is asked
		// again because the durable snapshot still points at the previous step.
		log.Printf("chat: persist question for run %s: %v", sess.runID, persistErr)
		if versionConflict {
			s.forgetSession(sess.runID)
		}
	}
}

// forgetSession drops the in-memory session so the next request reloads the
// newer durable state. It is used after an optimistic version conflict.
func (s *Service) forgetSession(runID string) {
	s.mu.Lock()
	delete(s.sessions, runID)
	s.mu.Unlock()
}

// failSession marks an active session as failed after an unrecoverable
// interviewer error, so the run cannot silently hang.
func (s *Service) failSession(sess *session) {
	sess.mu.Lock()
	if err := s.persistStep(sess, s.snapshotLocked(sess, SessionFailed, domain.RunFailed), nil); err != nil {
		log.Printf("chat: persist failed state for run %s: %v", sess.runID, err)
	}
	sess.finished = true
	sess.mu.Unlock()
	s.publish(sess.runID, Event{Type: "run_status", Status: string(domain.RunFailed)})
	s.notifyFinish(sess.runID, domain.RunFailed)
}

// finalizerTimeout bounds the post-interview analyst/synthesizer pipeline. It
// is intentionally generous: a long transcript and several source extractions
// can legitimately take tens of minutes.
const finalizerTimeout = 60 * time.Minute

func (s *Service) complete(sess *session) {
	status := domain.RunDone
	analysisUnavailable := false
	sess.mu.Lock()
	state := sess.session.State()
	sess.mu.Unlock()
	if finalizer := s.finalizerFn(); finalizer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), finalizerTimeout)
		// The finalizer makes the analyst and synthesizer LLM calls; the run
		// id in the context attributes them to this run.
		ctx = llmobs.WithRunID(ctx, sess.runID)
		finalStatus, err := finalizer(ctx, sess.runID, sess.profession, state)
		cancel()
		if err != nil {
			log.Printf("chat: finalizer failed for run %s: %v", sess.runID, err)
			status = domain.RunFailed
			analysisUnavailable = errors.Is(err, ErrAnalysisUnavailable)
			// A restart may interrupt the pipeline after the tree transaction was
			// committed but before this snapshot was written. Re-running the
			// finalizer then fails with TREE_ALREADY_EXISTS; the saved tree is the
			// authoritative success signal.
			if s.runHasTree(sess.runID) {
				log.Printf("chat: run %s already has a saved tree; keeping done status", sess.runID)
				status = domain.RunDone
			}
		} else if finalStatus != "" {
			status = finalStatus
		}
	}

	finalText := ""
	if status == domain.RunDone || status == domain.RunDegraded {
		if sess.learning {
			finalText = interviewer.FinalCoursesMessage(sess.profession)
		} else {
			finalText = "Интервью завершено. Результат исследования доступен во вкладке «Дерево»."
		}
	} else if status == domain.RunFailed && analysisUnavailable {
		if sess.learning {
			finalText = interviewer.FallbackRecommendationsMessage(state)
		} else {
			finalText = "Интервью завершено. Ответы сохранены. Анализ сейчас недоступен."
		}
	}

	sess.mu.Lock()
	snapshotStatus := SessionCompleted
	if status == domain.RunFailed {
		snapshotStatus = SessionFailed
	}
	var messages []Message
	if finalText != "" {
		messages = append(messages, newMessage(sess.runID, sess.seq+1, "agent", finalText))
	}
	if err := s.persistStep(sess, s.snapshotLocked(sess, snapshotStatus, status), messages); err != nil {
		// The terminal snapshot will be retried after a restart (the session stays
		// finalizing in the database), so the run is not lost.
		log.Printf("chat: persist final state for run %s: %v", sess.runID, err)
	} else {
		sess.messages = append(sess.messages, messages...)
		if len(messages) > 0 {
			sess.seq = messages[len(messages)-1].Sequence
		}
	}
	sess.finalizing = false
	sess.finished = true
	sess.mu.Unlock()

	if len(messages) > 0 {
		message := messages[len(messages)-1]
		s.publish(sess.runID, Event{Type: "agent_message", Message: &message})
	}
	s.publish(sess.runID, Event{Type: "run_status", Status: string(status)})
	s.notifyFinish(sess.runID, status)
}

// runHasTree reports whether the run already has a persisted activity tree.
// It is used to treat a re-run finalizer that hits TREE_ALREADY_EXISTS as a
// success instead of failing an already completed research.
func (s *Service) runHasTree(runID string) bool {
	checker, ok := s.sessionStoreOf().(runTreeStore)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	hasTree, err := checker.RunHasTree(ctx, runID)
	return err == nil && hasTree
}

func (s *Service) notifyFinish(runID string, status domain.RunStatus) {
	if s.onFinish != nil {
		s.onFinish(runID, status)
	}
}

// newMessage builds one durable chat message. Microsecond precision matches
// PostgreSQL TIMESTAMPTZ, so a message reloaded after a restart has exactly the
// timestamp it had in memory.
func newMessage(runID string, seq int, role, content string) Message {
	return Message{
		ID:        fmt.Sprintf("msg-%s-%d", runID, seq),
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		Sequence:  seq,
	}
}

// snapshotLocked captures the restorable session state. It must be called with
// sess.mu held.
func (s *Service) snapshotLocked(sess *session, status SessionStatus, finalStatus domain.RunStatus) SessionSnapshot {
	return SessionSnapshot{
		SchemaVersion: SessionSchemaVersion,
		Profession:    sess.profession,
		PromptVersion: sess.promptVersion,
		Status:        status,
		FinalStatus:   finalStatus,
		Learning:      sess.learning,
		Interviewer:   sess.session.Snapshot(),
		Version:       sess.version,
	}
}

// persistStep writes the session snapshot and the dialog messages atomically.
// With no store configured it keeps the in-memory-only demo behaviour; with only
// a MessageStore it writes just the messages (the pre-restoration behaviour).
// It must be called with sess.mu held.
func (s *Service) persistStep(sess *session, snapshot SessionSnapshot, messages []Message) error {
	store := s.sessionStoreOf()
	if store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		defer cancel()
		if err := store.SaveStep(ctx, sess.runID, snapshot, messages); err != nil {
			return err
		}
		sess.version = snapshot.Version + 1
		return nil
	}
	messageStore := s.messageStore()
	if messageStore == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	for _, message := range messages {
		if err := messageStore.AppendMessage(ctx, sess.runID, message); err != nil {
			return err
		}
	}
	return nil
}

// RecoverSessions inspects durable chat state once after a backend start:
// finalizing sessions are completed in the background, terminal sessions whose
// run row was not updated are reconciled, and chat runs that predate durable
// sessions are marked failed with SESSION_INTERRUPTED instead of hanging.
// It is safe to call when no session store is configured.
func (s *Service) RecoverSessions(ctx context.Context) {
	store := s.sessionStoreOf()
	if store == nil {
		return
	}
	recovery, ok := store.(sessionRecoveryStore)
	if !ok {
		return
	}

	// The last answer was stored but the post-interview pipeline never finished:
	// resume it so the run cannot stay in "running" forever.
	if ids, err := recovery.ListSessionsByStatus(ctx, SessionFinalizing, s.startedAt); err != nil {
		log.Printf("chat: recovery: list finalizing sessions: %v", err)
	} else {
		for _, runID := range ids {
			go s.resumeRun(runID)
		}
	}

	// A terminal snapshot may be newer than the run row if the process died
	// between the two writes.
	if rows, err := recovery.ListFinalizedSessionsWithActiveRuns(ctx, s.startedAt); err != nil {
		log.Printf("chat: recovery: list finalized sessions: %v", err)
	} else {
		for _, row := range rows {
			status := row.FinalStatus
			if !status.Valid() {
				status = domain.RunDone
			}
			log.Printf("chat: recovery: run %s has a finalized chat session; setting status %s", row.RunID, status)
			s.notifyFinish(row.RunID, status)
		}
	}

	// Chat runs from a backend version without durable session snapshots cannot
	// be resumed; mark them failed explicitly.
	if ids, err := recovery.ListInterruptedChatRuns(ctx, s.startedAt); err != nil {
		log.Printf("chat: recovery: list interrupted chat runs: %v", err)
	} else {
		for _, runID := range ids {
			log.Printf("chat: recovery: run %s cannot be resumed (SESSION_INTERRUPTED); marking failed", runID)
			s.notifyFinish(runID, domain.RunFailed)
		}
	}
}

// resumeRun lazily restores one run and continues its pending step.
func (s *Service) resumeRun(runID string) {
	sess, err := s.lookup(runID)
	if err != nil {
		log.Printf("chat: recovery: resume run %s: %v", runID, err)
		return
	}
	s.resume(sess)
}

func isTerminalRunStatus(status domain.RunStatus) bool {
	switch status {
	case domain.RunDone, domain.RunDegraded, domain.RunFailed:
		return true
	default:
		return false
	}
}

// lastSequence returns the last durable dialog position, or 0 for an empty
// history. Message ids embed the sequence, so the restored session keeps
// generating unique ids.
func lastSequence(messages []Message) int {
	last := 0
	for _, message := range messages {
		if message.Sequence > last {
			last = message.Sequence
		}
	}
	return last
}

func (s *Service) publish(runID string, event Event) {
	sess, err := s.lookup(runID)
	if err != nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.publishLocked(event)
}

func (sess *session) publishLocked(event Event) {
	for ch := range sess.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}
