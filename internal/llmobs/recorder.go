package llmobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"activity-descriptor/internal/llm"
)

// RecordStore is the subset of Store used by Recorder.
type RecordStore interface {
	CreateRequest(ctx context.Context, req Request) (string, error)
	SaveResponse(ctx context.Context, response Response) error
	SaveUsage(ctx context.Context, usage Usage) error
	FinishRequest(ctx context.Context, id, status, errorCode string, latencyMS int, finishedAt time.Time) error
}

// Recorder wraps an LLM client and writes request/response audit rows.
type Recorder struct {
	Next     llm.ChatClient
	Store    RecordStore
	Agent    string
	Provider string
	Model    string
}

// NewRecorder creates an audit-recording client.
func NewRecorder(next llm.ChatClient, store RecordStore, agent, provider, model string) *Recorder {
	return &Recorder{Next: next, Store: store, Agent: agent, Provider: provider, Model: model}
}

// Chat records the request before the provider call and the response after it.
// The run id attached to ctx with WithRunID is stored as llm_requests.run_id,
// so the request is attributed to that run; without it the column stays NULL.
// Audit failures never change the model answer: observability is best effort.
func (r *Recorder) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if r == nil || r.Next == nil {
		return "", fmt.Errorf("llmobs: recorder has no client")
	}
	started := time.Now().UTC()
	traceID := randomHex(16)
	requestID, auditErr := r.Store.CreateRequest(ctx, Request{
		TraceID:     traceID,
		RunID:       RunIDFromContext(ctx),
		Agent:       r.Agent,
		Provider:    r.Provider,
		Model:       r.Model,
		Messages:    marshalMessages(messages),
		Status:      "running",
		StartedAt:   &started,
		Attempt:     1,
		MaxAttempts: 1,
		RequestHash: hashMessages(messages),
	})
	if auditErr != nil {
		requestID = ""
	}

	var captured *llm.Usage
	ctx = llm.WithUsageCollector(ctx, func(usage llm.Usage) {
		copied := usage
		captured = &copied
	})

	answer, err := r.Next.Chat(ctx, messages)
	latency := int(time.Since(started).Milliseconds())
	status := "ok"
	errorCode := ""
	if err != nil {
		status = "error"
		errorCode = string(llm.Code(err))
	}
	if requestID != "" {
		if err == nil {
			_ = r.Store.SaveResponse(ctx, Response{
				RequestID:        requestID,
				RawText:          answer,
				ValidationStatus: "unvalidated",
				ReceivedAt:       time.Now().UTC(),
			})
		}
		_ = r.Store.FinishRequest(ctx, requestID, status, errorCode, latency, time.Now().UTC())
		if captured != nil {
			_ = r.Store.SaveUsage(ctx, Usage{
				RequestID:        requestID,
				PromptTokens:     captured.PromptTokens,
				CompletionTokens: captured.CompletionTokens,
				TotalTokens:      captured.TotalTokens,
				LatencyMS:        latency,
			})
		}
	}
	return answer, err
}

func marshalMessages(messages []llm.Message) []byte {
	data, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	return data
}

func hashMessages(messages []llm.Message) string {
	hash := sha256.Sum256(marshalMessages(messages))
	return hex.EncodeToString(hash[:])
}

func randomHex(n int) string {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
