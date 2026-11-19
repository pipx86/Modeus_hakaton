// Package llmobs stores and reads LLM observability data.
package llmobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrNotFound marks a missing request or prompt version.
var ErrNotFound = errors.New("llmobs: not found")

// Request is one stored LLM request.
type Request struct {
	ID              string          `json:"id"`
	TraceID         string          `json:"trace_id"`
	RunID           string          `json:"run_id,omitempty"`
	Agent           string          `json:"agent"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	PromptVersionID string          `json:"prompt_version_id,omitempty"`
	RequestHash     string          `json:"request_hash,omitempty"`
	Messages        json.RawMessage `json:"messages,omitempty"`
	Params          json.RawMessage `json:"params,omitempty"`
	Status          string          `json:"status"`
	ErrorCode       string          `json:"error_code,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	LatencyMS       int             `json:"latency_ms"`
	Attempt         int             `json:"attempt"`
	MaxAttempts     int             `json:"max_attempts"`
	CreatedAt       time.Time       `json:"created_at"`
	Response        *Response       `json:"response,omitempty"`
	Usage           *Usage          `json:"usage,omitempty"`
}

// Response is one stored LLM response.
type Response struct {
	RequestID        string          `json:"request_id"`
	RawText          string          `json:"raw_text,omitempty"`
	ParsedJSON       json.RawMessage `json:"parsed_json,omitempty"`
	ValidationStatus string          `json:"validation_status,omitempty"`
	RepairAttempt    int             `json:"repair_attempt"`
	FinishReason     string          `json:"finish_reason,omitempty"`
	ReceivedAt       time.Time       `json:"received_at"`
}

// Usage is token and cost usage for one request.
type Usage struct {
	RequestID        string    `json:"request_id"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CostUSD          *float64  `json:"cost_usd,omitempty"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	LatencyMS        int       `json:"latency_ms"`
	CreatedAt        time.Time `json:"created_at"`
}

// PromptVersion is one immutable prompt version.
type PromptVersion struct {
	ID           string     `json:"id"`
	Agent        string     `json:"agent"`
	Version      string     `json:"version"`
	PromptHash   string     `json:"prompt_hash"`
	SystemText   string     `json:"system_text"`
	ExamplesText string     `json:"examples_text,omitempty"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ActivatedAt  *time.Time `json:"activated_at,omitempty"`
}

// ListFilter filters request listing.
type ListFilter struct {
	RunID  string
	Agent  string
	Status string
	Limit  int
}

type database interface {
	db.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Store is a PostgreSQL repository for LLM observability.
type Store struct {
	pool database
}

// NewStore creates a repository.
func NewStore(pool database) *Store { return &Store{pool: pool} }

// CreateRequest inserts a request and returns its id.
func (s *Store) CreateRequest(ctx context.Context, req Request) (string, error) {
	if s == nil || s.pool == nil {
		return "", fmt.Errorf("llmobs: nil store")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO llm_requests
			(trace_id, run_id, agent, provider, model, prompt_version_id,
			 request_hash, messages, params, status, attempt, max_attempts, started_at)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, NULLIF($6, '')::text,
			NULLIF($7, '')::char(64), $8, $9, $10,
			GREATEST($11, 1), GREATEST($12, 1), COALESCE($13, now()))
		RETURNING id::text`,
		req.TraceID, req.RunID, req.Agent, req.Provider, req.Model,
		req.PromptVersionID, req.RequestHash, nullableJSON(req.Messages), nullableJSON(req.Params),
		defaultStatus(req.Status), req.Attempt, req.MaxAttempts, req.StartedAt,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("llmobs: create request: %w", err)
	}
	return id, nil
}

// FinishRequest updates status, error code, latency and finish time.
func (s *Store) FinishRequest(ctx context.Context, id, status, errorCode string, latencyMS int, finishedAt time.Time) error {
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE llm_requests
		SET status=$2, error_code=NULLIF($3, ''), latency_ms=$4, finished_at=$5
		WHERE id=$1::uuid`,
		id, defaultStatus(status), errorCode, latencyMS, finishedAt,
	)
	if err != nil {
		return fmt.Errorf("llmobs: finish request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveResponse upserts a response for a request.
func (s *Store) SaveResponse(ctx context.Context, response Response) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO llm_responses
			(request_id, raw_text, parsed_json, validation_status, repair_attempt, finish_reason, received_at)
		VALUES ($1::uuid, NULLIF($2, ''), $3, NULLIF($4, ''), GREATEST($5, 0), NULLIF($6, ''), COALESCE($7, now()))
		ON CONFLICT (request_id) DO UPDATE SET
			raw_text=EXCLUDED.raw_text, parsed_json=EXCLUDED.parsed_json,
			validation_status=EXCLUDED.validation_status, repair_attempt=EXCLUDED.repair_attempt,
			finish_reason=EXCLUDED.finish_reason, received_at=EXCLUDED.received_at`,
		response.RequestID, response.RawText, nullableJSON(response.ParsedJSON),
		response.ValidationStatus, response.RepairAttempt, response.FinishReason, nullableTime(response.ReceivedAt),
	)
	if err != nil {
		return fmt.Errorf("llmobs: save response: %w", err)
	}
	return nil
}

// SaveUsage upserts usage for a request.
func (s *Store) SaveUsage(ctx context.Context, usage Usage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO llm_usage
			(request_id, prompt_tokens, completion_tokens, total_tokens, cost_usd, reasoning_effort, latency_ms)
		VALUES ($1::uuid, GREATEST($2, 0), GREATEST($3, 0), GREATEST($4, 0), $5, NULLIF($6, ''), GREATEST($7, 0))
		ON CONFLICT (request_id) DO UPDATE SET
			prompt_tokens=EXCLUDED.prompt_tokens, completion_tokens=EXCLUDED.completion_tokens,
			total_tokens=EXCLUDED.total_tokens, cost_usd=EXCLUDED.cost_usd,
			reasoning_effort=EXCLUDED.reasoning_effort, latency_ms=EXCLUDED.latency_ms`,
		usage.RequestID, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
		usage.CostUSD, usage.ReasoningEffort, usage.LatencyMS,
	)
	if err != nil {
		return fmt.Errorf("llmobs: save usage: %w", err)
	}
	return nil
}

// GetRequest returns one request with response and usage.
func (s *Store) GetRequest(ctx context.Context, id string) (Request, error) {
	var req Request
	var runID, promptVersionID, requestHash, errorCode pgtype.Text
	var rawText, validationStatus, finishReason pgtype.Text
	var parsedJSON []byte
	var startedAt, finishedAt, receivedAt pgtype.Timestamptz
	var latencyMS, attempt, maxAttempts, repairAttempt pgtype.Int4
	var promptTokens, completionTokens, totalTokens, usageLatency pgtype.Int4
	var costUSD pgtype.Numeric
	var reasoningEffort pgtype.Text
	var responseRequestID pgtype.UUID
	var usageRequestID pgtype.UUID

	err := s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.trace_id, r.run_id::text, r.agent, r.provider, r.model,
			r.prompt_version_id, r.request_hash, r.status, r.error_code,
			r.started_at, r.finished_at, r.latency_ms, r.attempt, r.max_attempts, r.created_at,
			rr.request_id, rr.raw_text, rr.parsed_json, rr.validation_status, rr.repair_attempt,
			rr.finish_reason, rr.received_at,
			uu.request_id, uu.prompt_tokens, uu.completion_tokens, uu.total_tokens, uu.cost_usd,
			uu.reasoning_effort, uu.latency_ms
		FROM llm_requests r
		LEFT JOIN llm_responses rr ON rr.request_id=r.id
		LEFT JOIN llm_usage uu ON uu.request_id=r.id
		WHERE r.id=$1::uuid`, id,
	).Scan(
		&req.ID, &req.TraceID, &runID, &req.Agent, &req.Provider, &req.Model,
		&promptVersionID, &requestHash, &req.Status, &errorCode,
		&startedAt, &finishedAt, &latencyMS, &attempt, &maxAttempts, &req.CreatedAt,
		&responseRequestID, &rawText, &parsedJSON, &validationStatus, &repairAttempt,
		&finishReason, &receivedAt,
		&usageRequestID, &promptTokens, &completionTokens, &totalTokens, &costUSD,
		&reasoningEffort, &usageLatency,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("llmobs: get request: %w", err)
	}

	req.RunID = runID.String
	req.PromptVersionID = promptVersionID.String
	req.RequestHash = requestHash.String
	req.ErrorCode = errorCode.String
	req.StartedAt = timePtr(startedAt)
	req.FinishedAt = timePtr(finishedAt)
	req.LatencyMS = int(latencyMS.Int32)
	req.Attempt = int(attempt.Int32)
	req.MaxAttempts = int(maxAttempts.Int32)
	if responseRequestID.Valid {
		req.Response = &Response{
			RequestID:        responseRequestID.String(),
			RawText:          rawText.String,
			ParsedJSON:       parsedJSON,
			ValidationStatus: validationStatus.String,
			RepairAttempt:    int(repairAttempt.Int32),
			FinishReason:     finishReason.String,
			ReceivedAt:       timeValue(receivedAt),
		}
	}
	if usageRequestID.Valid {
		req.Usage = &Usage{
			RequestID:        usageRequestID.String(),
			PromptTokens:     int(promptTokens.Int32),
			CompletionTokens: int(completionTokens.Int32),
			TotalTokens:      int(totalTokens.Int32),
			CostUSD:          floatPtr(costUSD),
			ReasoningEffort:  reasoningEffort.String,
			LatencyMS:        int(usageLatency.Int32),
		}
	}
	return req, nil
}

// ListRequests returns requests matching filter, newest first.
func (s *Store) ListRequests(ctx context.Context, filter ListFilter) ([]Request, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, trace_id, run_id::text, agent, provider, model,
			prompt_version_id, request_hash, status, error_code,
			started_at, finished_at, latency_ms, attempt, max_attempts, created_at
		FROM llm_requests
		WHERE ($1 = '' OR run_id=$1::uuid)
		  AND ($2 = '' OR agent=$2)
		  AND ($3 = '' OR status=$3)
		ORDER BY created_at DESC
		LIMIT $4`, filter.RunID, filter.Agent, filter.Status, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("llmobs: list requests: %w", err)
	}
	defer rows.Close()

	var out []Request
	for rows.Next() {
		var req Request
		var runID, promptVersionID, requestHash, errorCode pgtype.Text
		var startedAt, finishedAt pgtype.Timestamptz
		var latencyMS, attempt, maxAttempts pgtype.Int4
		if err := rows.Scan(
			&req.ID, &req.TraceID, &runID, &req.Agent, &req.Provider, &req.Model,
			&promptVersionID, &requestHash, &req.Status, &errorCode,
			&startedAt, &finishedAt, &latencyMS, &attempt, &maxAttempts, &req.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("llmobs: scan request: %w", err)
		}
		req.RunID = runID.String
		req.PromptVersionID = promptVersionID.String
		req.RequestHash = requestHash.String
		req.ErrorCode = errorCode.String
		req.StartedAt = timePtr(startedAt)
		req.FinishedAt = timePtr(finishedAt)
		req.LatencyMS = int(latencyMS.Int32)
		req.Attempt = int(attempt.Int32)
		req.MaxAttempts = int(maxAttempts.Int32)
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("llmobs: list requests: %w", err)
	}
	return out, nil
}

// ListPromptVersions returns prompt versions for an agent.
func (s *Store) ListPromptVersions(ctx context.Context, agent string) ([]PromptVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent, version, prompt_hash, system_text, COALESCE(examples_text, ''),
			status, created_at, activated_at
		FROM prompt_versions
		WHERE ($1 = '' OR agent=$1)
		ORDER BY agent, created_at DESC`, agent,
	)
	if err != nil {
		return nil, fmt.Errorf("llmobs: list prompt versions: %w", err)
	}
	defer rows.Close()

	var out []PromptVersion
	for rows.Next() {
		var version PromptVersion
		var activatedAt pgtype.Timestamptz
		if err := rows.Scan(&version.ID, &version.Agent, &version.Version, &version.PromptHash,
			&version.SystemText, &version.ExamplesText, &version.Status, &version.CreatedAt, &activatedAt); err != nil {
			return nil, fmt.Errorf("llmobs: scan prompt version: %w", err)
		}
		version.ActivatedAt = timePtr(activatedAt)
		out = append(out, version)
	}
	return out, rows.Err()
}

// ActivePromptVersion returns the currently active prompt version for an agent.
func (s *Store) ActivePromptVersion(ctx context.Context, agent string) (PromptVersion, error) {
	var version PromptVersion
	var activatedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		SELECT id, agent, version, prompt_hash, system_text, COALESCE(examples_text, ''),
			status, created_at, activated_at
		FROM prompt_versions
		WHERE agent=$1 AND status='active'
		ORDER BY activated_at DESC NULLS LAST
		LIMIT 1`, agent,
	).Scan(&version.ID, &version.Agent, &version.Version, &version.PromptHash,
		&version.SystemText, &version.ExamplesText, &version.Status, &version.CreatedAt, &activatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PromptVersion{}, ErrNotFound
	}
	if err != nil {
		return PromptVersion{}, fmt.Errorf("llmobs: active prompt version: %w", err)
	}
	version.ActivatedAt = timePtr(activatedAt)
	return version, nil
}

// ActivatePromptVersion atomically switches the active version for its agent.
func (s *Store) ActivatePromptVersion(ctx context.Context, id, activatedBy, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("llmobs: begin activation: %w", err)
	}
	defer tx.Rollback(context.Background())

	var agent string
	if err := tx.QueryRow(ctx, `SELECT agent FROM prompt_versions WHERE id=$1`, id).Scan(&agent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("llmobs: find prompt version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE prompt_versions SET status='archived', activated_at=NULL
		WHERE agent=$1 AND status='active' AND id<>$2`, agent, id); err != nil {
		return fmt.Errorf("llmobs: archive active versions: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE prompt_versions SET status='active', activated_at=now()
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("llmobs: activate prompt version: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO prompt_activations(agent, prompt_version_id, activated_by, reason)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))`, agent, id, activatedBy, reason); err != nil {
		return fmt.Errorf("llmobs: record activation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("llmobs: commit activation: %w", err)
	}
	return nil
}

func nullableJSON(data json.RawMessage) any {
	if len(data) == 0 {
		return nil
	}
	return data
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func defaultStatus(status string) string {
	if status == "" {
		return "running"
	}
	return status
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func timeValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

func floatPtr(value pgtype.Numeric) *float64 {
	if !value.Valid {
		return nil
	}
	f, err := value.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}

// ModelUsage — агрегированный расход токенов по модели за период.
type ModelUsage struct {
	Model            string `json:"model"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
}

// UsageByModel суммирует токены по моделям за указанный период.
func (s *Store) UsageByModel(ctx context.Context, since time.Time) ([]ModelUsage, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("llmobs: nil store")
	}
	rows, err := s.pool.Query(ctx, `SELECT r.model,
       COUNT(*)::bigint,
       COALESCE(SUM(u.prompt_tokens),0)::bigint,
       COALESCE(SUM(u.completion_tokens),0)::bigint,
       COALESCE(SUM(u.total_tokens),0)::bigint
FROM llm_requests r
LEFT JOIN llm_usage u ON u.request_id = r.id
WHERE r.created_at >= $1
GROUP BY r.model
ORDER BY 5 DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("llmobs: usage by model: %w", err)
	}
	defer rows.Close()

	out := []ModelUsage{}
	for rows.Next() {
		var item ModelUsage
		if err := rows.Scan(&item.Model, &item.Requests, &item.PromptTokens, &item.CompletionTokens, &item.TotalTokens); err != nil {
			return nil, fmt.Errorf("llmobs: scan usage: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
