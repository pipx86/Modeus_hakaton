// Package chat keeps the interviewer dialog for one run, mirrors it to durable
// storage when configured, and exposes messages and events to the HTTP API.
//
// The active dialog (SSE events, in-flight interviewer questions) stays in
// memory, but every message is also written through MessageStore so that
// GET /api/v1/runs/{id}/messages keeps returning the history after a backend
// restart, when the in-memory session is gone.
package chat

import (
	"context"
	"errors"
	"fmt"
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

const welcomeMessage = "Здравствуйте! Я чат-интервьюер. Я задам вам несколько вопросов о вашей профессиональной деятельности, а затем подготовлю рекомендации и план развития."

// persistTimeout bounds one durable write/read of the chat history. The
// dialog itself must not hang on a slow database.
const persistTimeout = 5 * time.Second

// Service owns one interviewer session per run.
type Service struct {
	client        llm.ChatClient
	finalizer     Finalizer
	onFinish      func(runID string, status domain.RunStatus)
	promptVersion string

	mu       sync.Mutex
	sessions map[string]*session
	store    MessageStore
}

type session struct {
	mu          sync.Mutex
	session     *interviewer.Session
	messages    []Message
	seq         int
	asking      bool
	subscribers map[chan Event]struct{}
}

// NewService creates a chat service. client may be nil, in which case fallback
// questions are used. onFinish is optional.
func NewService(client llm.ChatClient, onFinish func(string, domain.RunStatus)) *Service {
	if client == nil {
		client = UnavailableClient{}
	}
	return &Service{
		client:   client,
		onFinish: onFinish,
		sessions: make(map[string]*session),
	}
}

// SetMessageStore installs the durable history store. It must be called before
// the first run is started. Nil (the default) keeps the dialog in memory only,
// which is the demo/test setup.
func (s *Service) SetMessageStore(store MessageStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}

// messageStore returns the configured persistence layer. It is read on every
// write so SetMessageStore is safe with a concurrently running service.
func (s *Service) messageStore() MessageStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store
}

// SetFinalizer installs the post-interview pipeline. It must be called before
// the first run is started.
func (s *Service) SetFinalizer(finalizer Finalizer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizer = finalizer
}

// SetPromptVersion pins the interviewer prompt version for new sessions.
func (s *Service) SetPromptVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptVersion = version
}

// Start creates the interview session and asks the first question.
func (s *Service) Start(runID, profession string) error {
	if runID == "" {
		return fmt.Errorf("chat: run id is required")
	}
	opts := []interviewer.Option{}
	if s.promptVersion != "" {
		opts = append(opts, interviewer.WithPromptVersion(s.promptVersion))
	}
	agent, err := interviewer.New(s.client, opts...)
	if err != nil {
		return fmt.Errorf("chat: interviewer: %w", err)
	}
	interview, err := interviewer.NewSession(agent, "run-"+runID, domain.Respondent{
		ID:         "respondent-" + runID,
		Role:       profession,
		Profession: profession,
	})
	if err != nil {
		return fmt.Errorf("chat: session: %w", err)
	}

	s.mu.Lock()
	if _, exists := s.sessions[runID]; exists {
		s.mu.Unlock()
		return nil
	}
	sess := &session{
		session:     interview,
		subscribers: make(map[chan Event]struct{}),
	}
	s.sessions[runID] = sess
	s.mu.Unlock()

	sess.mu.Lock()
	welcome, err := s.appendMessageLocked(runID, sess, "agent", welcomeMessage, true)
	if err != nil {
		sess.mu.Unlock()
		s.mu.Lock()
		delete(s.sessions, runID)
		s.mu.Unlock()
		return fmt.Errorf("chat: persist greeting: %w", err)
	}
	sess.asking = true
	sess.mu.Unlock()
	s.publish(runID, Event{Type: "agent_message", Message: &welcome})
	s.publish(runID, Event{Type: "agent_thinking"})
	go s.askNext(runID, sess)
	return nil
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
	if errors.Is(err, ErrNotFound) {
		return s.persistedMessages(runID)
	}
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]Message, len(sess.messages))
	copy(out, sess.messages)
	return out, nil
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
	message, err := s.appendMessageLocked(runID, sess, "user", content, true)
	if err != nil {
		sess.mu.Unlock()
		return Message{}, fmt.Errorf("chat: persist answer: %w", err)
	}
	if err := sess.session.Answer(content); err != nil {
		sess.mu.Unlock()
		return Message{}, fmt.Errorf("chat: answer: %w", err)
	}
	done := sess.session.Done()
	sess.mu.Unlock()

	if done {
		sess.mu.Lock()
		state := sess.session.Close()
		sess.mu.Unlock()
		s.publish(runID, Event{Type: "agent_thinking"})
		go s.complete(runID, state.Profession, state)
		return message, nil
	}

	sess.mu.Lock()
	if sess.asking {
		sess.mu.Unlock()
		return message, nil
	}
	sess.asking = true
	sess.mu.Unlock()
	s.publish(runID, Event{Type: "agent_thinking"})
	go s.askNext(runID, sess)
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
	return ch, cancel, nil
}

func (s *Service) lookup(runID string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[runID]
	if !ok {
		return nil, ErrNotFound
	}
	return sess, nil
}

func (s *Service) askNext(runID string, sess *session) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// Name the run in the audit context, so the LLM request rows written by
	// llmobs.Recorder carry llm_requests.run_id and the run cost endpoint can
	// attribute the interviewer calls exactly.
	ctx = llmobs.WithRunID(ctx, runID)

	sess.mu.Lock()
	question, err := sess.session.Next(ctx)
	if err != nil {
		sess.asking = false
		sess.mu.Unlock()
		if errors.Is(err, interviewer.ErrSessionDone) {
			return
		}
		s.publish(runID, Event{Type: "run_status", Status: string(domain.RunFailed)})
		if s.onFinish != nil {
			s.onFinish(runID, domain.RunFailed)
		}
		return
	}

	message, err := s.appendMessageLocked(runID, sess, "agent", question.Text, false)
	// Ответ на опубликованный вопрос может прийти сразу. Снимаем флаг до выдачи вопроса.
	sess.asking = false
	if err != nil {
		sess.mu.Unlock()
		log.Printf("chat: persist question for run %s: %v", runID, err)
		return
	}
	sess.publishLocked(Event{Type: "agent_message", Message: &message})
	sess.mu.Unlock()
}

func (s *Service) complete(runID, profession string, state domain.InterviewSession) {
	status := domain.RunDone
	if s.finalizer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		// The finalizer makes the analyst (and optional ZUN) LLM calls; the run
		// id in the context attributes them to this run.
		ctx = llmobs.WithRunID(ctx, runID)
		finalStatus, err := s.finalizer(ctx, runID, profession, state)
		cancel()
		if err != nil {
			log.Printf("chat: finalizer failed for run %s: %v", runID, err)
			status = domain.RunFailed
		} else if finalStatus != "" {
			status = finalStatus
		}
	}
	if status == domain.RunDone || status == domain.RunDegraded {
		if message := interviewer.FinalCoursesMessage(profession); message != "" {
			s.appendAgentMessage(runID, message)
		}
	}
	s.publish(runID, Event{Type: "run_status", Status: string(status)})
	if s.onFinish != nil {
		s.onFinish(runID, status)
	}
}

func (s *Service) appendAgentMessage(runID, content string) {
	sess, err := s.lookup(runID)
	if err != nil {
		return
	}
	sess.mu.Lock()
	message, err := s.appendMessageLocked(runID, sess, "agent", content, false)
	sess.mu.Unlock()
	if err != nil {
		log.Printf("chat: persist agent message for run %s: %v", runID, err)
		return
	}
	s.publish(runID, Event{Type: "agent_message", Message: &message})
}

// appendMessageLocked appends one message to the in-memory session and mirrors
// it into the durable store. It must be called with sess.mu held.
//
// required decides what a store failure means. For messages the respondent is
// waiting on (the greeting and user answers) the failure is returned and the
// sequence counter is rolled back, so memory never answers 201 for a message
// that was not persisted; the caller can retry with the same ID. Background
// agent messages are best effort: the error is logged and the message still
// reaches the active subscriber, so a database hiccup cannot stall the dialog.
func (s *Service) appendMessageLocked(runID string, sess *session, role, content string, required bool) (Message, error) {
	sess.seq++
	message := Message{
		ID:   fmt.Sprintf("msg-%s-%d", runID, sess.seq),
		Role: role,
		// Microsecond precision matches PostgreSQL TIMESTAMPTZ, so a message
		// reloaded after a restart has exactly the timestamp it had in memory.
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		Content:   content,
		Sequence:  sess.seq,
	}
	if store := s.messageStore(); store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		err := store.AppendMessage(ctx, runID, message)
		cancel()
		if err != nil {
			if required {
				sess.seq--
				return Message{}, err
			}
			log.Printf("chat: persist message for run %s: %v", runID, err)
		}
	}
	sess.messages = append(sess.messages, message)
	return message, nil
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
