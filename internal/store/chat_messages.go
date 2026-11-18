package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

// ChatMessageStore is the PostgreSQL implementation of chat.MessageStore. It
// keeps the interviewer dialog durable so chat history survives a backend
// restart and GET /api/v1/runs/{id}/messages keeps working for completed runs.
type ChatMessageStore struct {
	queries *db.Queries
}

// NewChatMessageStore accepts a pool or a connection: both satisfy db.DBTX.
func NewChatMessageStore(dbtx db.DBTX) *ChatMessageStore {
	return &ChatMessageStore{queries: db.New(dbtx)}
}

var _ chat.MessageStore = (*ChatMessageStore)(nil)

// AppendMessage stores one dialog message. Repeating the same (run_id,
// sequence) position is a no-op, so a retried write cannot duplicate a row.
func (s *ChatMessageStore) AppendMessage(ctx context.Context, runID string, message chat.Message) error {
	var id pgtype.UUID
	if err := id.Scan(runID); err != nil || !id.Valid {
		return fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}
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
	_, err := s.queries.InsertRunChatMessage(ctx, db.InsertRunChatMessageParams{
		ID:        message.ID,
		RunID:     id,
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

// ListMessages returns the run dialog ordered by sequence and reports
// chat.ErrNotFound when the run itself does not exist. An existing run without
// any messages yields an empty slice, not an error.
func (s *ChatMessageStore) ListMessages(ctx context.Context, runID string) ([]chat.Message, error) {
	var id pgtype.UUID
	if err := id.Scan(runID); err != nil || !id.Valid {
		return nil, fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}
	rows, err := s.queries.ListRunChatMessages(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	messages := make([]chat.Message, 0, len(rows))
	for _, row := range rows {
		message := chat.Message{
			ID:        row.ID,
			Role:      row.Role,
			Content:   row.Content,
			CreatedAt: row.CreatedAt.Time,
			Sequence:  int(row.Sequence),
		}
		if len(row.Citations) > 0 {
			if err := json.Unmarshal(row.Citations, &message.Citations); err != nil {
				return nil, fmt.Errorf("decode chat citations: %w", err)
			}
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		exists, err := s.queries.RunExists(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("check run exists: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
		}
	}
	return messages, nil
}
