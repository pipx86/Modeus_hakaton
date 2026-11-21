package interviewer

import (
	"errors"
	"fmt"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

// Snapshot is a serializable copy of the mutable state of one interview
// session. It exists so a restarted backend can rebuild the dialog exactly where
// it stopped: the question plan, the questions already asked, the respondent
// answers, the fallback counter and the clarification cursor are all preserved.
//
// Snapshot contains no pointers into the live session; it is safe to marshal to
// JSON, store in PostgreSQL and decode in another process.
type Snapshot struct {
	State        domain.InterviewSession `json:"state"`
	Plan         []domain.QuestionLevel  `json:"plan"`
	Extended     bool                    `json:"extended,omitempty"`
	Last         agent.InterviewerOutput `json:"last,omitempty"`
	LastError    string                  `json:"last_error,omitempty"`
	ClarifiedFor int                     `json:"clarified_for"`
	Fallbacks    int                     `json:"fallbacks"`
}

// Snapshot returns a deep copy of the session state. The result can be
// persisted and later passed to Restore or RestoreSession.
func (s *Session) Snapshot() Snapshot {
	snapshot := Snapshot{
		State:        cloneState(s.state),
		Plan:         append([]domain.QuestionLevel(nil), s.plan...),
		Extended:     s.extended,
		Last:         s.last,
		ClarifiedFor: s.clarifiedFor,
		Fallbacks:    s.fallbacks,
	}
	if s.lastErr != nil {
		snapshot.LastError = s.lastErr.Error()
	}
	return snapshot
}

// Restore replaces the session state with a previously captured Snapshot while
// keeping the configured agent. It validates the snapshot and returns an error
// for corrupt or incompatible state, so a broken row is reported instead of
// silently continuing a wrong dialog.
func (s *Session) Restore(snapshot Snapshot) error {
	if s == nil {
		return errors.New("interviewer: session is nil")
	}
	if s.agent == nil {
		return errors.New("interviewer: agent is required")
	}
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	s.state = cloneState(snapshot.State)
	s.plan = append([]domain.QuestionLevel(nil), snapshot.Plan...)
	s.extended = snapshot.Extended
	s.last = snapshot.Last
	s.clarifiedFor = snapshot.ClarifiedFor
	s.fallbacks = snapshot.Fallbacks
	// Question ids are generated from this counter as q-<seq>; it always equals
	// the number of questions already asked.
	s.seq = len(s.state.Questions)
	if snapshot.LastError != "" {
		s.lastErr = errors.New(snapshot.LastError)
	} else {
		s.lastErr = nil
	}
	return nil
}

// RestoreSession rebuilds an interview session from a Snapshot and the agent it
// should keep using. It is the cross-process counterpart of Session.Snapshot.
func RestoreSession(agentInstance *Agent, snapshot Snapshot) (*Session, error) {
	if agentInstance == nil {
		return nil, errors.New("interviewer: agent is required")
	}
	session := &Session{agent: agentInstance}
	if err := session.Restore(snapshot); err != nil {
		return nil, err
	}
	return session, nil
}

// PendingAnswer reports whether the last asked question still waits for a
// respondent answer. It is false right after a question is generated and true
// once that question has been answered.
func (s *Session) PendingAnswer() bool {
	return len(s.state.Answers) < len(s.state.Questions)
}

func validateSnapshot(snapshot Snapshot) error {
	state := snapshot.State
	if strings.TrimSpace(state.ID) == "" {
		return errors.New("interviewer: snapshot has empty session id")
	}
	if strings.TrimSpace(state.Profession) == "" {
		return errors.New("interviewer: snapshot has empty profession")
	}
	if len(snapshot.Plan) == 0 {
		return errors.New("interviewer: snapshot has empty question plan")
	}
	for index, level := range snapshot.Plan {
		if !level.Valid() {
			return fmt.Errorf("interviewer: snapshot plan[%d] has invalid level %q", index, level)
		}
	}
	if len(state.Questions) > len(snapshot.Plan) {
		return fmt.Errorf("interviewer: snapshot has %d questions for a plan of %d", len(state.Questions), len(snapshot.Plan))
	}
	if len(state.Answers) > len(state.Questions) {
		return fmt.Errorf("interviewer: snapshot has %d answers for %d questions", len(state.Answers), len(state.Questions))
	}
	if snapshot.Extended && (len(snapshot.Plan) != CoreQuestionCount+2 || len(state.Answers) < CoreQuestionCount) {
		return errors.New("interviewer: snapshot has invalid extended plan")
	}
	if snapshot.ClarifiedFor < -1 {
		return fmt.Errorf("interviewer: snapshot has invalid clarification cursor %d", snapshot.ClarifiedFor)
	}
	if snapshot.ClarifiedFor >= len(state.Answers) && snapshot.ClarifiedFor != -1 {
		return fmt.Errorf("interviewer: snapshot clarification cursor %d is outside %d answers", snapshot.ClarifiedFor, len(state.Answers))
	}
	if snapshot.Fallbacks < 0 || snapshot.Fallbacks > maxFallbackQuestions {
		return fmt.Errorf("interviewer: snapshot has invalid fallback counter %d", snapshot.Fallbacks)
	}
	switch state.Status {
	case domain.SessionPending, domain.SessionInProgress, domain.SessionDone, domain.SessionDegraded, domain.SessionFailed:
	default:
		return fmt.Errorf("interviewer: snapshot has invalid session status %q", state.Status)
	}
	return nil
}

// cloneState copies an InterviewSession deeply enough that later mutations of
// the live session (appending questions/answers) cannot leak into the snapshot
// and vice versa.
func cloneState(state domain.InterviewSession) domain.InterviewSession {
	cloned := state
	cloned.Questions = append([]domain.Question(nil), state.Questions...)
	for index := range cloned.Questions {
		if cloned.Questions[index].ParentID != nil {
			parentID := *cloned.Questions[index].ParentID
			cloned.Questions[index].ParentID = &parentID
		}
	}
	cloned.Answers = append([]domain.Answer(nil), state.Answers...)
	return cloned
}
