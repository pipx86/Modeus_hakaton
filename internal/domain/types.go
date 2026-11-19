package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrEvidenceRequired = errors.New("EVIDENCE_REQUIRED")

// Evidence field validation errors used by the AI agent layer.
var (
	ErrEvidenceSource = errors.New("EVIDENCE_SOURCE_REQUIRED")
	ErrEvidenceQuote  = errors.New("EVIDENCE_QUOTE_REQUIRED")
	ErrEvidenceRef    = errors.New("EVIDENCE_REF_REQUIRED")
)

type NodeType string

const (
	NodeRole     NodeType = "role"
	NodeFunction NodeType = "function"
	NodeTask     NodeType = "task"
	NodeAction   NodeType = "action"
)

// Valid reports whether t is a known node type.
func (t NodeType) Valid() bool {
	switch t {
	case NodeRole, NodeFunction, NodeTask, NodeAction:
		return true
	default:
		return false
	}
}

type SourceType string

const (
	SourceVacancy   SourceType = "vacancy"
	SourceInterview SourceType = "interview"
	SourceStandard  SourceType = "standard"
	SourceDocument  SourceType = "document"
)

// Source trust priorities. Higher wins when sources disagree.
const (
	PriorityInterview = 300
	PriorityStandard  = 200
	PriorityVacancy   = 100
	PriorityDocument  = 50
)

// SourcePriority returns the trust priority of a source type.
func SourcePriority(t SourceType) int {
	switch t {
	case SourceInterview:
		return PriorityInterview
	case SourceStandard:
		return PriorityStandard
	case SourceVacancy:
		return PriorityVacancy
	case SourceDocument:
		return PriorityDocument
	default:
		return 0
	}
}

// String returns the source type as a plain string.
func (t SourceType) String() string { return string(t) }

type RunStatus string

const (
	RunPending  RunStatus = "pending"
	RunRunning  RunStatus = "running"
	RunDone     RunStatus = "done"
	RunDegraded RunStatus = "degraded"
	RunFailed   RunStatus = "failed"
)

// Valid reports whether s is a supported run status.
func (s RunStatus) Valid() bool {
	switch s {
	case RunPending, RunRunning, RunDone, RunDegraded, RunFailed:
		return true
	default:
		return false
	}
}

// EvidenceStatus is the review state of a quote attached to a tree node.
type EvidenceStatus string

const (
	EvidencePending  EvidenceStatus = "pending"
	EvidenceApproved EvidenceStatus = "approved"
	EvidenceRejected EvidenceStatus = "rejected"
)

func (s EvidenceStatus) Valid() bool {
	switch s {
	case EvidencePending, EvidenceApproved, EvidenceRejected, "":
		return true
	default:
		return false
	}
}

type Evidence struct {
	ID       string         `json:"id"`
	NodeID   string         `json:"node_id,omitempty"`
	SourceID string         `json:"source_id"`
	RefID    string         `json:"ref_id,omitempty"`
	Quote    string         `json:"quote"`
	Status   EvidenceStatus `json:"status,omitempty"`
}

// Validate enforces the fields required by the AI agent contract.
func (e Evidence) Validate() error {
	switch {
	case strings.TrimSpace(e.SourceID) == "":
		return ErrEvidenceSource
	case strings.TrimSpace(e.Quote) == "":
		return ErrEvidenceQuote
	case strings.TrimSpace(e.RefID) == "":
		return ErrEvidenceRef
	default:
		return nil
	}
}

type Node struct {
	ID          string     `json:"id"`
	Type        NodeType   `json:"type"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	ParentID    *string    `json:"parent_id,omitempty"`
	Evidence    []Evidence `json:"evidence"`
	Zun         *Zun       `json:"zun,omitempty"`
}

// Zun — знания, умения и навыки, необходимые для выполнения узла дерева.
type Zun struct {
	Knowledge []string `json:"knowledge"`
	Skills    []string `json:"skills"`
	Abilities []string `json:"abilities"`
}

func (n Node) Validate() error {
	if len(n.Evidence) == 0 {
		return ErrEvidenceRequired
	}
	return nil
}

// User — учётная запись с паролем; role всегда admin или user.
type User struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	PasswordHash string `json:"-"`
}

type Source struct {
	ID        string     `json:"id"`
	Type      SourceType `json:"type"`
	Title     string     `json:"title"`
	Content   string     `json:"content,omitempty"`
	SourceURL string     `json:"source_url,omitempty"`
	Priority  int        `json:"priority,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// EffectivePriority returns the explicit priority when set, otherwise the
// default priority of the source type.
func (s Source) EffectivePriority() int {
	if s.Priority > 0 {
		return s.Priority
	}
	return SourcePriority(s.Type)
}

type RunType string

const (
	RunTypeBatch RunType = "batch"
	RunTypeChat  RunType = "chat"
)

type Run struct {
	ID               string             `json:"id"`
	Status           RunStatus          `json:"status"`
	Profession       string             `json:"profession"`
	Type             RunType            `json:"type,omitempty"`
	RootNodeID       *string            `json:"root_node_id,omitempty"`
	StartedAt        *time.Time         `json:"started_at,omitempty"`
	FinishedAt       *time.Time         `json:"finished_at,omitempty"`
	Stage            Stage              `json:"stage,omitempty"`
	SkillFreq        map[string]int     `json:"skill_freq,omitempty"`
	HybridWeightsRaw map[string]float64 `json:"hybrid_weights_raw,omitempty"`
	PerVacancySkills [][]string         `json:"-"`
	LevelData        []map[string]any   `json:"level_data,omitempty"`
	LevelSkills      [][]string         `json:"-"`
	Data             map[string]any     `json:"-"`
}

type Stage string

const (
	StageCreated   Stage = "created"
	StageInterview Stage = "interview"
	StageAnalysis  Stage = "analysis"
	StageSynthesis Stage = "synthesis"
	StageReview    Stage = "review"
	StageDone      Stage = "done"
	StageFailed    Stage = "failed"
)

var ErrInvalidTransition = errors.New("INVALID_TRANSITION")

var allowedTransitions = map[Stage][]Stage{
	StageCreated:   {StageInterview, StageFailed},
	StageInterview: {StageAnalysis, StageFailed},
	StageAnalysis:  {StageSynthesis, StageFailed},
	StageSynthesis: {StageReview, StageFailed},
	StageReview:    {StageDone, StageFailed},
	StageDone:      {},
	StageFailed:    {},
}

func CanTransition(from, to Stage) bool {
	// Resume: разрешаем повторный запуск текущей стадии после перезапуска сервера.
	if from == to {
		return true
	}
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Respondent is the person interviewed about a role.
type Respondent struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role,omitempty"`
	Level      string `json:"level,omitempty"`
	Profession string `json:"profession,omitempty"`
}

// QuestionLevel is the activity-tree level a question is aimed at.
type QuestionLevel string

const (
	QuestionRole     QuestionLevel = "role"
	QuestionFunction QuestionLevel = "function"
	QuestionTask     QuestionLevel = "task"
	QuestionAction   QuestionLevel = "action"
)

// Valid reports whether l is a known question level.
func (l QuestionLevel) Valid() bool {
	switch l {
	case QuestionRole, QuestionFunction, QuestionTask, QuestionAction:
		return true
	default:
		return false
	}
}

// Question is one interviewer question.
type Question struct {
	ID            string        `json:"id"`
	Level         QuestionLevel `json:"level"`
	Text          string        `json:"text"`
	Rationale     string        `json:"rationale,omitempty"`
	ParentID      *string       `json:"parent_id,omitempty"`
	Clarification bool          `json:"clarification,omitempty"`
	Fallback      bool          `json:"fallback,omitempty"`
}

// Answer is a respondent's reply to a question.
type Answer struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text"`
	Vague      bool   `json:"vague,omitempty"`
}

// SessionStatus is the lifecycle state of an interview session.
type SessionStatus string

const (
	SessionPending    SessionStatus = "pending"
	SessionInProgress SessionStatus = "in_progress"
	SessionDone       SessionStatus = "done"
	SessionDegraded   SessionStatus = "degraded"
	SessionFailed     SessionStatus = "failed"
)

// InterviewSession is the persisted state of one interview.
type InterviewSession struct {
	ID         string        `json:"id"`
	Respondent Respondent    `json:"respondent"`
	Profession string        `json:"profession,omitempty"`
	Status     SessionStatus `json:"status"`
	Questions  []Question    `json:"questions,omitempty"`
	Answers    []Answer      `json:"answers,omitempty"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
}

// ConflictValue is one competing value for a node field.
type ConflictValue struct {
	SourceID   string     `json:"source_id"`
	SourceType SourceType `json:"source_type"`
	Value      string     `json:"value"`
	Priority   int        `json:"priority"`
}

// Conflict records a disagreement between sources and its resolution.
type Conflict struct {
	ID     string          `json:"id"`
	NodeID string          `json:"node_id"`
	Field  string          `json:"field"`
	Values []ConflictValue `json:"values"`
	Winner ConflictValue   `json:"winner"`
	Reason string          `json:"reason"`
}

// ResolveConflict picks the value with the highest source priority.
func ResolveConflict(values []ConflictValue) (ConflictValue, string) {
	if len(values) == 0 {
		return ConflictValue{}, "no values to resolve"
	}
	winner := values[0]
	for _, v := range values[1:] {
		if v.Priority > winner.Priority {
			winner = v
		}
	}
	return winner, "higher source priority wins: " + winner.SourceType.String() + " (" + itoa(winner.Priority) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
