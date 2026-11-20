package domain

import "time"

// ResultKind — ожидаемый результат исследования (карточка 25): только описание
// деятельности или ещё и сопоставление с образовательной программой.
type ResultKind string

const (
	ResultKindActivity ResultKind = "activity"
	ResultKindProgram  ResultKind = "program"
)

// NodeReviewStatus — решение методиста по элементу дерева (карточка 18).
type NodeReviewStatus string

const (
	NodeReviewPending  NodeReviewStatus = "pending"
	NodeReviewApproved NodeReviewStatus = "approved"
	NodeReviewRejected NodeReviewStatus = "rejected"
)

// ValidNodeReviewDecision сообщает, можно ли вынести такое решение. Статус
// pending — исходное состояние, его нельзя выставить через API проверки.
func ValidNodeReviewDecision(value string) bool {
	switch NodeReviewStatus(value) {
	case NodeReviewApproved, NodeReviewRejected:
		return true
	default:
		return false
	}
}

// ProgramLinkStatus — состояние связи узла деятельности с элементом программы
// (карточки 21–23). proposed означает предложение системы, а не факт.
type ProgramLinkStatus string

const (
	ProgramLinkProposed ProgramLinkStatus = "proposed"
	ProgramLinkApproved ProgramLinkStatus = "approved"
	ProgramLinkRejected ProgramLinkStatus = "rejected"
)

// ValidProgramLinkDecision — решения, которые методист может вынести по связи.
func ValidProgramLinkDecision(value string) bool {
	switch ProgramLinkStatus(value) {
	case ProgramLinkApproved, ProgramLinkRejected:
		return true
	default:
		return false
	}
}

// ProgramLink — предложенная или подтверждённая связь узла дерева с
// компетенцией (и, если она известна, с конкретным KSA) образовательной
// программы. Reason объясняет, почему система предложила связь.
type ProgramLink struct {
	ID              string            `json:"id"`
	RunID           string            `json:"run_id"`
	NodeID          string            `json:"node_id"`
	NodeTitle       string            `json:"node_title"`
	NodeType        NodeType          `json:"node_type"`
	ProgramID       string            `json:"program_id"`
	ProgramTitle    string            `json:"program_title"`
	CompetencyID    string            `json:"competency_id"`
	CompetencyTitle string            `json:"competency_title"`
	KsaID           string            `json:"ksa_id,omitempty"`
	KsaTitle        string            `json:"ksa_title,omitempty"`
	Status          ProgramLinkStatus `json:"status"`
	Reason          string            `json:"reason"`
	Source          string            `json:"source"`
	ReviewedAt      *time.Time        `json:"reviewed_at,omitempty"`
}

// ReviewCounts — распределение элементов по статусам проверки.
type ReviewCounts struct {
	Pending  int `json:"pending"`
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
	Total    int `json:"total"`
}

// LinkCounts — распределение связей с программой. У предложений ядра
// отдельное имя статуса, поэтому их нельзя смешивать с "не проверено" узлов.
type LinkCounts struct {
	Proposed int `json:"proposed"`
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
	Total    int `json:"total"`
}

// ReviewSummary — что уже проверено методистом в исследовании (карточка 26).
type ReviewSummary struct {
	Nodes    ReviewCounts `json:"nodes"`
	Evidence ReviewCounts `json:"evidence"`
	Links    LinkCounts   `json:"links"`
}

// NodeReviewResult — ответ на решение по узлу. Содержит и id, и node_id, чтобы
// клиенту не приходилось угадывать имя поля.
type NodeReviewResult struct {
	ID            string           `json:"id"`
	NodeID        string           `json:"node_id"`
	ReviewStatus  NodeReviewStatus `json:"review_status"`
	ReviewComment string           `json:"review_comment"`
	ReviewedAt    *time.Time       `json:"reviewed_at,omitempty"`
}
