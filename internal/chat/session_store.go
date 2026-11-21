package chat

import (
	"context"
	"errors"
	"time"

	"activity-descriptor/internal/agent/interviewer"
	"activity-descriptor/internal/domain"
)

// SessionSchemaVersion is the version of the SessionSnapshot JSON persisted in
// run_chat_sessions.state. A backend that meets an unknown version must not
// guess how to restore the dialog: it reports the session as interrupted
// instead of silently resuming with the wrong cursor.
const SessionSchemaVersion = 1

// SessionStatus is the durable lifecycle of one chat session row.
type SessionStatus string

const (
	// SessionActive means the interviewer waits for the respondent or is about
	// to ask the next question.
	SessionActive SessionStatus = "active"
	// SessionFinalizing means every answer is collected and the post-interview
	// pipeline (analyst, synthesizer, tree) is running or must be resumed.
	SessionFinalizing SessionStatus = "finalizing"
	// SessionCompleted means the run finished successfully (done or degraded).
	SessionCompleted SessionStatus = "completed"
	// SessionFailed means the session ended with an explicit failure.
	SessionFailed SessionStatus = "failed"
)

// SessionSnapshot is everything a restarted backend needs to continue one chat
// interview: the serialized interviewer state plus the run-level metadata. It
// is stored as JSONB in run_chat_sessions.state.
type SessionSnapshot struct {
	SchemaVersion        int                  `json:"schema_version"`
	Profession           string               `json:"profession"`
	PromptVersion        string               `json:"prompt_version,omitempty"`
	Status               SessionStatus        `json:"status"`
	Learning             bool                 `json:"learning,omitempty"`
	AwaitingContinuation bool                 `json:"awaiting_continuation,omitempty"`
	FinalStatus          domain.RunStatus     `json:"final_status,omitempty"`
	Interviewer          interviewer.Snapshot `json:"interviewer"`

	// Version is the optimistic concurrency token of the row. It is read from
	// the version column and never serialized into the state JSON.
	Version int64 `json:"-"`
}

// SessionStore persists the interviewer session snapshot and the dialog
// messages. A restarted backend lazily reloads the snapshot through it and
// continues the dialog instead of leaving an active run hanging.
//
// SaveStep must update the snapshot and append messages in one transaction and
// must enforce snapshot.Version: expected 0 inserts a new row, any other value
// updates the row only when its current version matches. A version mismatch is
// reported as ErrVersionConflict so two concurrent writers cannot advance the
// same interview twice.
//
// Implementations are expected to satisfy MessageStore as well, so the same
// adapter serves the read-only history fallback for legacy runs.
type SessionStore interface {
	MessageStore

	// LoadSession returns the stored snapshot or ErrNotFound when the run has no
	// durable session at all.
	LoadSession(ctx context.Context, runID string) (SessionSnapshot, error)
	// SaveStep atomically persists the snapshot and the dialog messages.
	SaveStep(ctx context.Context, runID string, snapshot SessionSnapshot, messages []Message) error
	// RunStatus returns the current status of the run, or ErrNotFound when the
	// run does not exist.
	RunStatus(ctx context.Context, runID string) (domain.RunStatus, error)
}

// sessionRecoveryStore is the optional extra surface used by RecoverSessions
// after a backend restart. Adapters that cannot list runs may omit it.
//
// Every method takes a cutoff timestamp captured when the service started: only
// state that predates this process can have been interrupted by its start, so
// recovery never touches runs created after startup (for example by a second
// backend instance or a parallel test).
type sessionRecoveryStore interface {
	// ListSessionsByStatus returns run ids created before createdBefore whose
	// chat session has status.
	ListSessionsByStatus(ctx context.Context, status SessionStatus, createdBefore time.Time) ([]string, error)
	// ListFinalizedSessionsWithActiveRuns returns terminal sessions last updated
	// before updatedBefore whose run row still looks active because the process
	// died before the status transition.
	ListFinalizedSessionsWithActiveRuns(ctx context.Context, updatedBefore time.Time) ([]FinalizedSession, error)
	// ListInterruptedChatRuns returns active chat runs created before
	// createdBefore without any durable session row; such legacy runs cannot be
	// resumed and are marked failed.
	ListInterruptedChatRuns(ctx context.Context, createdBefore time.Time) ([]string, error)
}

// runTreeStore is the optional extra surface used to avoid marking a run failed
// when the interview pipeline already persisted its tree before a restart.
type runTreeStore interface {
	RunHasTree(ctx context.Context, runID string) (bool, error)
}

// FinalizedSession identifies one terminal chat session whose run row still
// looks active.
type FinalizedSession struct {
	RunID       string
	FinalStatus domain.RunStatus
}

var (
	// ErrVersionConflict means another writer advanced the durable session since
	// this process loaded it. The caller must reload instead of overwriting.
	ErrVersionConflict = errors.New("chat: session version conflict")
	// ErrSessionFinished means the interview is already over and its history is
	// read-only.
	ErrSessionFinished = errors.New("chat: session is finished")
	// ErrSessionInterrupted means an active chat run cannot be restored (legacy
	// run without a snapshot or a corrupt snapshot). The run is marked failed;
	// POST returns this explicit error instead of hanging.
	ErrSessionInterrupted = errors.New("chat: session interrupted")
	// ErrQuestionPending means the interviewer is still preparing the next
	// question, so there is no open question to answer yet.
	ErrQuestionPending = errors.New("chat: question is pending")
	// ErrContinuationUnavailable means the interview is not waiting for a choice.
	ErrContinuationUnavailable = errors.New("chat: continuation is unavailable")
)

// persistTimeout bounds one durable write/read of the chat history. The
// dialog itself must not hang on a slow database.
const persistTimeout = 5 * time.Second
