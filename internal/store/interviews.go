package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// Interview registry of one research run.
//
// A run used to have exactly one chat session (run_chat_sessions.run_id was the
// primary key), so a second respondent could not exist. run_interviews now
// holds one row per respondent interview, the session row is the cursor of the
// *active* interview, and run_chat_messages carries interview_id so the dialogs
// never mix.

// StartInterview closes the active interview of the run, opens a new one for
// the given respondent and drops the stale session cursor, so the interviewer
// starts a fresh dialog instead of resuming the previous respondent.
func (s *ChatSessionStore) StartInterview(ctx context.Context, runID string, respondent domain.Respondent) (chat.Interview, error) {
	id, err := parseRunUUID(runID)
	if err != nil {
		return chat.Interview{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return chat.Interview{}, fmt.Errorf("begin interview: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	queries := db.New(tx)

	exists, err := queries.RunExists(ctx, id)
	if err != nil {
		return chat.Interview{}, fmt.Errorf("check run exists: %w", err)
	}
	if !exists {
		return chat.Interview{}, fmt.Errorf("%w: run id %q", chat.ErrNotFound, runID)
	}

	// Only one active interview per run is allowed by the partial unique index;
	// closing first keeps the transition atomic.
	if _, err = queries.CloseActiveRunInterviews(ctx, id); err != nil {
		return chat.Interview{}, fmt.Errorf("close active interviews: %w", err)
	}
	row, err := queries.CreateRunInterview(ctx, db.CreateRunInterviewParams{
		RunID:          id,
		RespondentName: respondent.Name,
		RespondentRole: respondent.Role,
		RespondentRef:  respondent.ID,
	})
	if err != nil {
		return chat.Interview{}, fmt.Errorf("create interview: %w", err)
	}
	// The previous dialog cursor belongs to the previous respondent. Its
	// messages stay in run_chat_messages with their interview_id.
	if _, err = queries.DeleteRunChatSession(ctx, id); err != nil {
		return chat.Interview{}, fmt.Errorf("reset chat session cursor: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return chat.Interview{}, fmt.Errorf("commit interview: %w", err)
	}
	return interviewFromRow(row), nil
}

// ListInterviews returns every interview of the run, oldest first.
func (s *ChatSessionStore) ListInterviews(ctx context.Context, runID string) ([]chat.Interview, error) {
	id, err := parseRunUUID(runID)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(s.pool).ListRunInterviews(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list interviews: %w", err)
	}
	out := make([]chat.Interview, 0, len(rows))
	for _, row := range rows {
		out = append(out, interviewFromRow(row))
	}
	return out, nil
}

// GetInterview returns one interview of the run, or chat.ErrNotFound when the
// interview does not belong to that run.
func (s *ChatSessionStore) GetInterview(ctx context.Context, runID, interviewID string) (chat.Interview, error) {
	runUUID, err := parseRunUUID(runID)
	if err != nil {
		return chat.Interview{}, err
	}
	interviewUUID, err := parseRunUUID(interviewID)
	if err != nil {
		return chat.Interview{}, err
	}
	row, err := db.New(s.pool).GetRunInterview(ctx, db.GetRunInterviewParams{
		ID:    interviewUUID,
		RunID: runUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.Interview{}, fmt.Errorf("%w: interview id %q", chat.ErrNotFound, interviewID)
	}
	if err != nil {
		return chat.Interview{}, fmt.Errorf("get interview: %w", err)
	}
	return interviewFromRow(row), nil
}

// FailActiveInterview marks the active interview as failed. It is used when the
// interviewer dialog cannot be restarted, so the run is not left with an
// interview that pretends to be in progress.
func (s *ChatSessionStore) FailActiveInterview(ctx context.Context, runID string) error {
	id, err := parseRunUUID(runID)
	if err != nil {
		return err
	}
	if _, err = db.New(s.pool).FailActiveRunInterviews(ctx, id); err != nil {
		return fmt.Errorf("fail active interview: %w", err)
	}
	return nil
}

// InterviewMessages returns the dialog of one interview, ordered by sequence.
// Messages of other respondents of the same run are excluded.
func (s *ChatSessionStore) InterviewMessages(ctx context.Context, runID, interviewID string) ([]chat.Message, error) {
	runUUID, err := parseRunUUID(runID)
	if err != nil {
		return nil, err
	}
	interviewUUID, err := parseRunUUID(interviewID)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(s.pool).ListRunChatMessagesByInterview(ctx, db.ListRunChatMessagesByInterviewParams{
		RunID:       runUUID,
		InterviewID: interviewUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list interview messages: %w", err)
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
				return nil, fmt.Errorf("decode interview citations: %w", err)
			}
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func interviewFromRow(row db.RunInterview) chat.Interview {
	interview := chat.Interview{
		ID:             row.ID.String(),
		RunID:          row.RunID.String(),
		RespondentName: row.RespondentName,
		RespondentRole: row.RespondentRole,
		RespondentRef:  row.RespondentRef,
		Status:         chat.InterviewStatus(row.Status),
		StartedAt:      row.StartedAt.Time,
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time
		interview.FinishedAt = &finished
	}
	return interview
}
