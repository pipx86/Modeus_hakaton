package llmobs

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStoreIntegration exercises request/response/usage persistence against a
// real PostgreSQL database. It is skipped when neither TEST_DATABASE_URL nor
// DATABASE_URL is configured, so the default unit-test job stays hermetic.
func TestStoreIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	suffix := time.Now().UnixNano()
	agent := "analyst"
	trace := fmt.Sprintf("integration-trace-%d", suffix)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM llm_responses WHERE request_id IN (SELECT id FROM llm_requests WHERE trace_id=$1)`, trace)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM llm_usage WHERE request_id IN (SELECT id FROM llm_requests WHERE trace_id=$1)`, trace)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM llm_requests WHERE trace_id=$1`, trace)
	})

	store := NewStore(pool)
	requestID, err := store.CreateRequest(ctx, Request{
		TraceID:  trace,
		Agent:    agent,
		Provider: "integration-test",
		Model:    "test-model",
		Messages: []byte(`[{"role":"user","content":"hello"}]`),
		Params:   []byte(`{"temperature":0}`),
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if requestID == "" {
		t.Fatal("CreateRequest returned empty id")
	}
	if err := store.SaveResponse(ctx, Response{
		RequestID:        requestID,
		RawText:          `{"ok":true}`,
		ParsedJSON:       []byte(`{"ok":true}`),
		ValidationStatus: "valid",
		FinishReason:     "stop",
	}); err != nil {
		t.Fatalf("SaveResponse: %v", err)
	}
	if err := store.SaveUsage(ctx, Usage{
		RequestID:        requestID,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		ReasoningEffort:  "low",
		LatencyMS:        12,
	}); err != nil {
		t.Fatalf("SaveUsage: %v", err)
	}
	if err := store.FinishRequest(ctx, requestID, "ok", "", 12, time.Now().UTC()); err != nil {
		t.Fatalf("FinishRequest: %v", err)
	}

	got, err := store.GetRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.TraceID != trace || got.Response == nil || got.Usage == nil {
		t.Fatalf("GetRequest = %#v", got)
	}
	if got.Usage.PromptTokens != 10 || got.Response.ValidationStatus != "valid" {
		t.Fatalf("nested data missing: %#v", got)
	}

	listed, err := store.ListRequests(ctx, ListFilter{Agent: agent, Limit: 5})
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	found := false
	for _, item := range listed {
		if item.ID == requestID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListRequests does not contain %s", requestID)
	}
}
