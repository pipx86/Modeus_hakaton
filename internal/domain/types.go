package domain

import (
	"errors"
	"time"
)

var ErrEvidenceRequired = errors.New("EVIDENCE_REQUIRED")

type NodeType string

const (
	NodeRole     NodeType = "role"
	NodeFunction NodeType = "function"
	NodeTask     NodeType = "task"
	NodeAction   NodeType = "action"
)

type SourceType string

const (
	SourceVacancy   SourceType = "vacancy"
	SourceInterview SourceType = "interview"
	SourceStandard  SourceType = "standard"
	SourceDocument  SourceType = "document"
)

type RunStatus string

const (
	RunPending RunStatus = "pending"
	RunRunning RunStatus = "running"
	RunDone    RunStatus = "done"
	RunFailed  RunStatus = "failed"
)

type Evidence struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
}

type Node struct {
	ID          string     `json:"id"`
	Type        NodeType   `json:"type"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	ParentID    *string    `json:"parent_id,omitempty"`
	Evidence    []Evidence `json:"evidence"`
}

func (n Node) Validate() error {
	if len(n.Evidence) == 0 {
		return ErrEvidenceRequired
	}
	return nil
}

type Source struct {
	ID        string     `json:"id"`
	Type      SourceType `json:"type"`
	Title     string     `json:"title"`
	Content   string     `json:"content,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type Run struct {
	ID         string     `json:"id"`
	Status     RunStatus  `json:"status"`
	Profession string     `json:"profession"`
	RootNodeID *string    `json:"root_node_id,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Stage      Stage      `json:"stage,omitempty"`
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
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}
