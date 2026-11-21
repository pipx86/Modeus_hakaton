package interviewer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

// ErrSessionDone marks a Next call after the question plan is exhausted.
var ErrSessionDone = errors.New("interviewer: session is done")

// ErrNoOpenQuestion marks an Answer call without a pending question.
var ErrNoOpenQuestion = errors.New("interviewer: no open question")

// maxFallbackQuestions stops the interview early when the provider keeps
// failing, instead of showing the respondent an endless run of templates.
const maxFallbackQuestions = 4

// Session drives one interview dialog. It never aborts because of a model
// failure: the failure is recorded in LastError, the run degrades to fallback
// questions and the dialog continues.
type Session struct {
	agent   *Agent
	state   domain.InterviewSession
	plan    []domain.QuestionLevel
	last    agent.InterviewerOutput
	lastErr error
	// clarifiedFor is the answer index a clarification has already been asked
	// for, so one vague answer is clarified at most once.
	clarifiedFor int
	fallbacks    int
	seq          int
	// sourceContext is a compact, trusted-by-position but untrusted-by-content
	// summary of vacancies and materials relevant to the profession.
	sourceContext string
	// learning selects the beginner learning interview instead of the expert
	// activity interview.
	learning bool
}

// SetLearning switches the session to a beginner learning interview.
func (s *Session) SetLearning(learning bool) {
	if s == nil {
		return
	}
	s.learning = learning
}

// SetSourceContext attaches vacancy/material context to the next questions.
// The text is treated as reference data, never as instructions.
func (s *Session) SetSourceContext(context string) {
	if s == nil {
		return
	}
	s.sourceContext = strings.TrimSpace(context)
}

// SetProfileContext attaches public GitHub project data to the session. It is
// used to ask better questions and is persisted with the interview snapshot.
func (s *Session) SetProfileContext(context string) {
	if s == nil {
		return
	}
	s.state.ProfileContext = strings.TrimSpace(context)
}

// SetProfileSkills stores the skills confirmed by the public GitHub profile so
// the interviewer can notice contradictions and ask about them.
func (s *Session) SetProfileSkills(skills []string) {
	if s == nil {
		return
	}
	s.state.ProfileSkills = append([]string(nil), skills...)
}

// NewSession starts an interview session for respondent. id must be unique.
func NewSession(agentInstance *Agent, id string, respondent domain.Respondent) (*Session, error) {
	if agentInstance == nil {
		return nil, errors.New("interviewer: agent is required")
	}
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("interviewer: session id is required")
	}
	return &Session{
		agent: agentInstance,
		state: domain.InterviewSession{
			ID:         id,
			Respondent: respondent,
			Profession: respondent.Profession,
			Status:     domain.SessionPending,
		},
		plan:         defaultPlan(),
		clarifiedFor: -1,
	}, nil
}

// State returns a snapshot of the persisted session state.
func (s *Session) State() domain.InterviewSession {
	snapshot := s.state
	snapshot.Questions = append([]domain.Question(nil), s.state.Questions...)
	snapshot.Answers = append([]domain.Answer(nil), s.state.Answers...)
	return snapshot
}

// Last returns the last interviewer output, including fallback reasoning.
func (s *Session) Last() agent.InterviewerOutput { return s.last }

// LastError returns the last model failure, if any. A non-nil error means the
// session is running in degraded mode.
func (s *Session) LastError() error { return s.lastErr }

// Done reports whether the question plan is exhausted or the model has
// degraded to the fallback limit.
func (s *Session) Done() bool {
	if len(s.state.Questions) >= len(s.plan) || s.fallbacks >= maxFallbackQuestions {
		return true
	}
	return false
}

// Next asks the model for the next question and records it. Model errors are
// not returned: the session falls back to a deterministic question. Only
// programming errors (nil context, exhausted plan) are returned.
func (s *Session) Next(ctx context.Context) (domain.Question, error) {
	if ctx == nil {
		return domain.Question{}, errors.New("interviewer: nil context")
	}
	if s.Done() {
		return domain.Question{}, ErrSessionDone
	}

	level := s.plan[len(s.state.Questions)]
	// Existing six-question snapshots retain their clarification behavior.
	// The four-question interview follows its four levels without spending one
	// of the respondent's turns on a follow-up.
	clarify := len(s.plan) > 4 && s.needsClarification()
	progress := s.Progress()
	in := agent.InterviewerInput{
		SessionID:         s.state.ID,
		Respondent:        s.state.Respondent,
		Profession:        s.state.Profession,
		SourceContext:     s.sourceContext,
		Asked:             append([]domain.Question(nil), s.state.Questions...),
		Answers:           append([]domain.Answer(nil), s.state.Answers...),
		NextLevel:         level,
		NeedClarification: clarify,
		MaxQuestions:      len(s.plan),
		ProfileContext:    s.state.ProfileContext,
		ProfileSkills:     append([]string(nil), s.state.ProfileSkills...),
		LastQuality:       progress.LastQuality,
		UncoveredTopics:   progress.UncoveredTopics,
		Contradictions:    progress.Contradictions,
		TopicTemplate:     TemplateFor(s.state.Profession).Topics,
		Learning:          s.learning,
	}

	out, err := s.agent.AskNext(ctx, in)
	if err != nil {
		s.lastErr = err
		s.fallbacks++
		out = fallbackOutput(level, s.state.Profession, clarify, s.state.Answers)
		s.state.Status = domain.SessionDegraded
	}

	s.normalize(&out, level, clarify)
	s.last = out
	s.state.Questions = append(s.state.Questions, out.Question)
	if s.state.Status != domain.SessionDegraded {
		s.state.Status = domain.SessionInProgress
	}
	return out.Question, nil
}

// Answer records the respondent's answer to the last question. The session
// flags vague answers; the next Next call asks a clarification for them.
func (s *Session) Answer(text string) error {
	if len(s.state.Answers) >= len(s.state.Questions) {
		return ErrNoOpenQuestion
	}
	lastQuestion := s.state.Questions[len(s.state.Questions)-1]
	quality, notes := ScoreAnswer(text)
	contradictions := Contradictions(s.state.ProfileSkills, text)
	for _, skill := range contradictions {
		found := false
		for _, existing := range s.state.Contradictions {
			if existing == skill {
				found = true
				break
			}
		}
		if !found {
			s.state.Contradictions = append(s.state.Contradictions, skill)
		}
	}
	s.state.Answers = append(s.state.Answers, domain.Answer{
		QuestionID:   lastQuestion.ID,
		Text:         text,
		Vague:        IsVague(text),
		Quality:      quality.Total,
		QualityNotes: notes,
	})
	if s.state.Status == domain.SessionPending {
		s.state.Status = domain.SessionInProgress
	}
	return nil
}

// Close finishes the session and returns its final state. A session that ever
// degraded keeps the degraded status.
func (s *Session) Close() domain.InterviewSession {
	if s.state.Status == domain.SessionDegraded {
		return s.State()
	}
	s.state.Status = domain.SessionDone
	return s.State()
}

func (s *Session) normalize(out *agent.InterviewerOutput, level domain.QuestionLevel, clarify bool) {
	s.seq++
	out.Question.ID = fmt.Sprintf("q-%d", s.seq)
	if !out.Question.Level.Valid() || len(s.plan) == 4 {
		out.Question.Level = level
	}
	if strings.TrimSpace(out.Question.Text) == "" {
		fallback := FallbackQuestion(level, s.state.Profession)
		out.Question.Text = fallback.Text
		out.Question.Rationale = fallback.Rationale
		out.Question.Fallback = true
	}
	out.Question.Clarification = clarify
	out.IsClarification = clarify
	if clarify && out.Question.Level == "" {
		out.Question.Level = level
	}
}

// needsClarification reports whether the latest answer needs a follow-up and
// records the answer as clarified so the question is asked only once.
func (s *Session) needsClarification() bool {
	index := len(s.state.Answers) - 1
	if index < 0 || index == s.clarifiedFor {
		return false
	}
	if !s.state.Answers[index].Vague {
		return false
	}
	if last := s.state.Questions[len(s.state.Questions)-1]; last.Clarification {
		return false
	}
	s.clarifiedFor = index
	return true
}

func fallbackOutput(level domain.QuestionLevel, profession string, clarify bool, answers []domain.Answer) agent.InterviewerOutput {
	if clarify && len(answers) > 0 {
		question := ClarificationQuestion(answers[len(answers)-1].Text)
		return agent.InterviewerOutput{
			Question:        question,
			Rationale:       question.Rationale,
			IsClarification: true,
		}
	}
	question := FallbackQuestion(level, profession)
	return agent.InterviewerOutput{
		Question:  question,
		Rationale: question.Rationale,
	}
}
