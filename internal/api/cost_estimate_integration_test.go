package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

// costAttributionTestPool opens the PostgreSQL database used by the cost
// attribution test. The convention matches internal/llmobs: TEST_DATABASE_URL
// first, then DATABASE_URL; the test is skipped when neither is set or when the
// optional LLM observability migration is not applied.
func costAttributionTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL/DATABASE_URL is not set: PostgreSQL cost attribution test skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ready bool
	err = pool.QueryRow(ctx, `SELECT to_regclass('public.llm_requests') IS NOT NULL
       AND to_regclass('public.llm_usage') IS NOT NULL
       AND EXISTS (
           SELECT 1 FROM pg_attribute
           WHERE attrelid = to_regclass('public.runs') AND attname = 'type' AND NOT attisdropped
       )
       AND EXISTS (
           SELECT 1 FROM pg_attribute
           WHERE attrelid = to_regclass('public.runs') AND attname = 'stage' AND NOT attisdropped
       )`).Scan(&ready)
	if err != nil || !ready {
		t.Skip("LLM observability tables are not migrated in this database: PostgreSQL cost attribution test skipped")
	}
	return pool
}

// TestRunLLMFactsAndCostHistoryAttributionPostgres covers the two attribution
// paths against real rows:
//
//  1. a request written with llm_requests.run_id is attributed exactly and wins
//     over any time-window candidate;
//  2. a legacy request without run_id is attributed through the run window;
//  3. CostHistory uses the same precedence for the estimator samples.
func TestRunLLMFactsAndCostHistoryAttributionPostgres(t *testing.T) {
	pool := costAttributionTestPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)

	unique := time.Now().UTC().Format("20060102150405.000000000")
	profession := "cost-attribution-" + unique
	tracePrefix := "cost-attribution-" + unique + "-"
	firstStart := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	firstFinish := firstStart.Add(2 * time.Minute)
	secondStart := firstFinish.Add(time.Minute)
	secondFinish := secondStart.Add(2 * time.Minute)

	insertRun := func(profession string, started, finished time.Time) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO runs (profession, status, stage, type, started_at, finished_at)
VALUES ($1, 'done', 'done', 'batch', $2, $3)
RETURNING id::text`, profession, started, finished).Scan(&id)
		if err != nil {
			t.Fatalf("insert run: %v", err)
		}
		return id
	}
	insertRequest := func(runID any, traceID string, createdAt, startedAt, finishedAt time.Time, tokens int, costUSD any) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO llm_requests
    (trace_id, run_id, agent, provider, model, status, started_at, finished_at, latency_ms, created_at)
VALUES ($1, $2, 'analyst', 'test-provider', 'test-model', 'ok', $3, $4, 1000, $5)
RETURNING id::text`, traceID, runID, startedAt, finishedAt, createdAt).Scan(&id)
		if err != nil {
			t.Fatalf("insert llm_requests: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO llm_usage
    (request_id, prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms)
VALUES ($1::uuid, $2, $3, $4, $5, 1000)`, id, tokens, 10, tokens+10, costUSD); err != nil {
			t.Fatalf("insert llm_usage: %v", err)
		}
		return id
	}

	firstID := insertRun(profession, firstStart, firstFinish)
	secondID := insertRun(profession, secondStart, secondFinish)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM llm_requests WHERE trace_id LIKE $1`, tracePrefix+"%")
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM runs WHERE id IN ($1::uuid, $2::uuid)`, firstID, secondID)
	})

	// Exact attribution for the first run.
	exactRequestID := insertRequest(firstID, tracePrefix+"exact", firstStart.Add(10*time.Second),
		firstStart.Add(10*time.Second), firstStart.Add(11*time.Second), 150, 0.0015)
	// A legacy (run_id NULL) request inside the first window must NOT be
	// attributed to the first run, because exact rows take precedence.
	insertRequest(nil, tracePrefix+"legacy-first", firstStart.Add(20*time.Second),
		firstStart.Add(20*time.Second), firstStart.Add(21*time.Second), 777, nil)
	// A legacy request inside the second window is the time-window fallback.
	legacySecondID := insertRequest(nil, tracePrefix+"legacy-second", secondStart.Add(30*time.Second),
		secondStart.Add(30*time.Second), secondStart.Add(31*time.Second), 350, nil)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM llm_requests WHERE id IN ($1::uuid, $2::uuid)`, exactRequestID, legacySecondID)
	})

	firstFacts, err := store.RunLLMFacts(ctx, &domain.Run{
		ID: firstID, Profession: profession, Type: domain.RunTypeBatch, Status: domain.RunDone,
		StartedAt: &firstStart, FinishedAt: &firstFinish,
	})
	if err != nil {
		t.Fatalf("RunLLMFacts(first): %v", err)
	}
	if firstFacts.Attribution != estimateAttributionRunID {
		t.Fatalf("first attribution = %q, want %q", firstFacts.Attribution, estimateAttributionRunID)
	}
	if len(firstFacts.Requests) != 1 || firstFacts.Requests[0].TotalTokens != 160 {
		t.Fatalf("first requests = %+v, want exactly the run_id row with 160 tokens", firstFacts.Requests)
	}

	secondFacts, err := store.RunLLMFacts(ctx, &domain.Run{
		ID: secondID, Profession: profession, Type: domain.RunTypeBatch, Status: domain.RunDone,
		StartedAt: &secondStart, FinishedAt: &secondFinish,
	})
	if err != nil {
		t.Fatalf("RunLLMFacts(second): %v", err)
	}
	if secondFacts.Attribution != estimateAttributionTimeWindow {
		t.Fatalf("second attribution = %q, want %q", secondFacts.Attribution, estimateAttributionTimeWindow)
	}
	if secondFacts.Ambiguous {
		t.Fatal("sequential runs must not be reported as ambiguous")
	}
	if len(secondFacts.Requests) != 1 || secondFacts.Requests[0].TotalTokens != 360 {
		t.Fatalf("second requests = %+v, want the legacy row with 360 tokens", secondFacts.Requests)
	}

	// The estimator history applies the same precedence and is case-insensitive.
	history, err := store.CostHistory(ctx, strings.ToUpper(profession), domain.RunTypeBatch)
	if err != nil {
		t.Fatalf("CostHistory: %v", err)
	}
	samples := map[string]CostRunSample{}
	for _, sample := range history.ProfessionRuns {
		samples[sample.RunID] = sample
	}
	firstSample, ok := samples[firstID]
	if !ok {
		t.Fatalf("first run is missing from the history sample: %+v", history.ProfessionRuns)
	}
	if firstSample.Attribution != estimateAttributionRunID || firstSample.TotalTokens != 160 {
		t.Fatalf("first sample = %+v, want run_id attribution and 160 tokens", firstSample)
	}
	secondSample, ok := samples[secondID]
	if !ok {
		t.Fatalf("second run is missing from the history sample: %+v", history.ProfessionRuns)
	}
	if secondSample.Attribution != estimateAttributionTimeWindow || secondSample.TotalTokens != 360 {
		t.Fatalf("second sample = %+v, want time_window attribution and 360 tokens", secondSample)
	}
}
