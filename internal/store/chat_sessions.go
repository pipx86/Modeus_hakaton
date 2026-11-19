package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ChatSessionStore is the PostgreSQL implementation of chat.SessionStore. It
// persists the interviewer session snapshot together with the dialog messages in
// one transaction, which is what lets a restarted backend continue an active
// interview at the exact question/answer cursor.
//
// It also implements chat.MessageStore, so the same adapter serves the
// read-only history fallback for completed and legacy runs.
type ChatSessionStore struct {
	pool     *pgxpool.Pool
	messages *ChatMessageStore
}

// NewChatSessionStore accepts the shared pgx pool used by the server.
func NewChatSessionStore(pool *pgxpool.Pool) *ChatSessionStore {
	return &ChatSessionStore{pool: pool, messages: NewChatMessageStore(pool)}
}

var _ chat.SessionStore = (*ChatSessionStore)(nil)

// AppendMessage delegates the durable dialog write to ChatMessageStore.
func (s *ChatSessionStore) AppendMessage(ctx context.Context, runID string, message chat.Message) error {
	return s.messages.AppendMessage(ctx, runID, message)
}

// ListMessages returns the run dialog ordered by sequence.
func (s *ChatSessionStore) ListMessages(ctx context.Context, runID string) ([]chat.Message, error) {
	return s.messages.ListMessages(ctx, runID)
}

// LoadSession returns the durable snapshot of one interviewer session.
func (s *ChatSessionStore) LoadSession(ctx context.Context, runID string) (chat.SessionSnapshot, error) {
	id, err := parseRunUUID(runID)
	if err != nil {
		return chat.SessionSnapshot{}, err
	}
	row, err := db.New(s.pool).GetRunChatSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.SessionSnapshot{}, fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}
	if err != nil {
		return chat.SessionSnapshot{}, fmt.Errorf("get chat session: %w", err)
	}
	var snapshot chat.SessionSnapshot
	if err := json.Unmarshal(row.State, &snapshot); err != nil {
		return chat.SessionSnapshot{}, fmt.Errorf("decode chat session state: %w", err)
	}
	snapshot.Version = row.Version
	return snapshot, nil
}

// SaveStep updates the session snapshot and appends the dialog messages in one
// transaction. The optimistic version column makes a concurrent or stale writer
// fail with chat.ErrVersionConflict instead of advancing the interview twice.
func (s *ChatSessionStore) SaveStep(ctx context.Context, runID string, snapshot chat.SessionSnapshot, messages []chat.Message) error {
	id, err := parseRunUUID(runID)
	if err != nil {
		return err
	}
	state, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode chat session state: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin chat session step: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	queries := db.New(tx)

	if snapshot.Version == 0 {
		rows, err := queries.InsertRunChatSession(ctx, db.InsertRunChatSessionParams{
			RunID:         id,
			SchemaVersion: int32(snapshot.SchemaVersion),
			Profession:    snapshot.Profession,
			PromptVersion: snapshot.PromptVersion,
			Status:        string(snapshot.Status),
			State:         state,
		})
		if err != nil {
			return fmt.Errorf("insert chat session: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("%w: run %q already has a chat session", chat.ErrVersionConflict, runID)
		}
	} else {
		rows, err := queries.UpdateRunChatSession(ctx, db.UpdateRunChatSessionParams{
			RunID:         id,
			SchemaVersion: int32(snapshot.SchemaVersion),
			Profession:    snapshot.Profession,
			PromptVersion: snapshot.PromptVersion,
			Status:        string(snapshot.Status),
			State:         state,
			Version:       snapshot.Version,
		})
		if err != nil {
			return fmt.Errorf("update chat session: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("%w: run %q version %d", chat.ErrVersionConflict, runID, snapshot.Version)
		}
	}
	for _, message := range messages {
		if err := insertRunChatMessage(ctx, queries, id, message); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit chat session step: %w", err)
	}
	return nil
}

// RunStatus returns the current status of the run row.
func (s *ChatSessionStore) RunStatus(ctx context.Context, runID string) (domain.RunStatus, error) {
	id, err := parseRunUUID(runID)
	if err != nil {
		return "", err
	}
	status, err := db.New(s.pool).GetRunChatRunStatus(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}
	if err != nil {
		return "", fmt.Errorf("get run status: %w", err)
	}
	return domain.RunStatus(status), nil
}

// RunHasTree reports whether the run already has a persisted activity tree.
func (s *ChatSessionStore) RunHasTree(ctx context.Context, runID string) (bool, error) {
	id, err := parseRunUUID(runID)
	if err != nil {
		return false, err
	}
	hasTree, err := db.New(s.pool).GetRunHasTree(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}
	if err != nil {
		return false, fmt.Errorf("get run tree existence: %w", err)
	}
	return hasTree, nil
}

// ListSessionsByStatus returns the run ids of chat sessions in status whose run
// was created before createdBefore.
func (s *ChatSessionStore) ListSessionsByStatus(ctx context.Context, status chat.SessionStatus, createdBefore time.Time) ([]string, error) {
	runIDs, err := db.New(s.pool).ListRunChatSessionsByStatus(ctx, db.ListRunChatSessionsByStatusParams{
		Status:    string(status),
		CreatedAt: pgtype.Timestamptz{Time: createdBefore, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list chat sessions by status: %w", err)
	}
	return runIDs, nil
}

// ListFinalizedSessionsWithActiveRuns returns terminal sessions updated before
// updatedBefore whose run row still looks active (the process died between the
// two writes).
func (s *ChatSessionStore) ListFinalizedSessionsWithActiveRuns(ctx context.Context, updatedBefore time.Time) ([]chat.FinalizedSession, error) {
	rows, err := db.New(s.pool).ListFinalizedRunChatSessionsWithActiveRuns(ctx, pgtype.Timestamptz{Time: updatedBefore, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list finalized chat sessions: %w", err)
	}
	out := make([]chat.FinalizedSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, chat.FinalizedSession{
			RunID:       row.RunID,
			FinalStatus: domain.RunStatus(row.FinalStatus),
		})
	}
	return out, nil
}

// ListInterruptedChatRuns returns active chat runs created before createdBefore
// without any durable session snapshot: they were created by a backend version
// that did not persist the interviewer cursor and cannot be resumed.
func (s *ChatSessionStore) ListInterruptedChatRuns(ctx context.Context, createdBefore time.Time) ([]string, error) {
	runIDs, err := db.New(s.pool).ListInterruptedChatRuns(ctx, pgtype.Timestamptz{Time: createdBefore, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list interrupted chat runs: %w", err)
	}
	return runIDs, nil
}

// parseRunUUID validates a run id before it reaches PostgreSQL.
func parseRunUUID(runID string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(runID); err != nil || !id.Valid {
		return pgtype.UUID{}, fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}
	return id, nil
}

// insertRunChatMessage writes one dialog message through the given query set
// (a pool or a transaction). Repeating the same (run_id, sequence) position is
// a no-op, so a retried step cannot duplicate a message.
func insertRunChatMessage(ctx context.Context, queries *db.Queries, runID pgtype.UUID, message chat.Message) error {
	if message.Sequence <= 0 {
		return errors.New("chat message sequence must be positive")
	}
	var citations []byte
	if len(message.Citations) > 0 {
		encoded, err := json.Marshal(message.Citations)
		if err != nil {
			return fmt.Errorf("marshal chat citations: %w", err)
		}
		citations = encoded
	}
	createdAt := message.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := queries.InsertRunChatMessage(ctx, db.InsertRunChatMessageParams{
		ID:        message.ID,
		RunID:     runID,
		Role:      message.Role,
		Content:   message.Content,
		Sequence:  int32(message.Sequence),
		Citations: citations,
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("insert chat message: %w", err)
	}
	return nil
}
