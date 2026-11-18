// Package chat keeps the in-memory interviewer dialog for one run and exposes
// messages and events to the HTTP API.
package chat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

// Message is one chat message. Role is user or agent.
type Message struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
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

// Service owns one interviewer session per run.
type Service struct {
	client        llm.ChatClient
	finalizer     Finalizer
	onFinish      func(runID string, status domain.RunStatus)
	promptVersion string

	mu       sync.Mutex
	sessions map[string]*session
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
	sess.seq++
	welcome := Message{
		ID:        fmt.Sprintf("msg-%s-%d", runID, sess.seq),
		Role:      "agent",
		Content:   welcomeMessage,
		CreatedAt: time.Now().UTC(),
	}
	sess.messages = append(sess.messages, welcome)
	sess.asking = true
	sess.mu.Unlock()
	s.publish(runID, Event{Type: "agent_message", Message: &welcome})
	s.publish(runID, Event{Type: "agent_thinking"})
	go s.askNext(runID, sess)
	return nil
}

// Messages returns a copy of the chat history.
func (s *Service) Messages(runID string) ([]Message, error) {
	sess, err := s.lookup(runID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	out := make([]Message, len(sess.messages))
	copy(out, sess.messages)
	return out, nil
}

// Post records the respondent answer and schedules the next question.
func (s *Service) Post(runID, content string) (Message, error) {
	if content == "" {
		return Message{}, ErrEmptyMessage
	}
	sess, err := s.lookup(runID)
	if err != nil {
		return Message{}, err
	}

	sess.mu.Lock()
	sess.seq++
	message := Message{
		ID:        fmt.Sprintf("msg-%s-%d", runID, sess.seq),
		Role:      "user",
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	sess.messages = append(sess.messages, message)
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
	defer func() {
		sess.mu.Lock()
		sess.asking = false
		sess.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	sess.mu.Lock()
	question, err := sess.session.Next(ctx)
	sess.mu.Unlock()
	if err != nil {
		if errors.Is(err, interviewer.ErrSessionDone) {
			return
		}
		s.publish(runID, Event{Type: "run_status", Status: string(domain.RunFailed)})
		if s.onFinish != nil {
			s.onFinish(runID, domain.RunFailed)
		}
		return
	}

	sess.mu.Lock()
	sess.seq++
	message := Message{
		ID:        fmt.Sprintf("msg-%s-%d", runID, sess.seq),
		Role:      "agent",
		Content:   question.Text,
		CreatedAt: time.Now().UTC(),
	}
	sess.messages = append(sess.messages, message)
	sess.mu.Unlock()

	s.publish(runID, Event{Type: "agent_message", Message: &message})
}

func (s *Service) complete(runID, profession string, state domain.InterviewSession) {
	status := domain.RunDone
	if s.finalizer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		finalStatus, err := s.finalizer(ctx, runID, profession, state)
		if err != nil {
			log.Printf("chat: finalizer failed for run %s: %v", runID, err)
			status = domain.RunFailed
		} else if finalStatus != "" {
			status = finalStatus
		}
	}
	s.publish(runID, Event{Type: "run_status", Status: string(status)})
	if s.onFinish != nil {
		s.onFinish(runID, status)
	}
}

func (s *Service) publish(runID string, event Event) {
	sess, err := s.lookup(runID)
	if err != nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for ch := range sess.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}
