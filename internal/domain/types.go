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
	RefID    string `json:"ref_id,omitempty"`
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
}
