package api

// PostgreSQL implementation of CostDataSource: historical samples for the
// pre-run estimate and the LLM facts of one run.
//
// # Attribution
//
// The exact attribution is llm_requests.run_id. The recorder fills it from the
// call context (llmobs.WithRunID) for the chat interview/finalizer and the
// batch analyst steps; legacy rows written before that wiring, or by binaries
// without the recorder, still have run_id NULL. For a single run
// (GET /runs/{id}/cost) such rows are attributed by creation time inside the
// run window [started_at, finished_at] and the response says
// attribution="time_window". When another run's window overlaps, the response
// also sets attribution_ambiguous=true; the numbers stay useful but may mix
// parallel runs.
//
// The historical sample for the pre-run estimate is stricter: a run whose
// window overlaps any other run contributes its duration but not its LLM usage,
// because counting a request for two historical runs would inflate the estimate.
// Exact run_id rows always win over the window fallback.
//
// # Failure modes
//
// The llm_observability migration is applied in production, but the rest of the
// API works without it (cmd/server does not require those tables). If the LLM
// tables are missing (PostgreSQL error 42P01), the queries degrade to
// duration-only history and empty run facts instead of failing the endpoint.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// llmRequestFactSelect is the shared projection of one attributed LLM request.
// It must match scanLLMRequestFacts.
const llmRequestFactSelect = `SELECT req.agent, req.model, req.status,
       COALESCE(usage.prompt_tokens, 0),
       COALESCE(usage.completion_tokens, 0),
       COALESCE(usage.total_tokens, 0),
       COALESCE(req.latency_ms, 0),
       usage.cost_usd
FROM llm_requests req
LEFT JOIN llm_usage usage ON usage.request_id = req.id `

// CostHistory implements CostDataSource.
func (p *PGStore) CostHistory(ctx context.Context, profession string, runType domain.RunType) (CostHistory, error) {
	if p == nil || p.Pool == nil {
		return CostHistory{}, errors.New("api: cost history requires a PostgreSQL store")
	}
	ctx = ensureCostContext(ctx)
	history := CostHistory{
		ProfessionRuns: []CostRunSample{},
		GlobalRuns:     []CostRunSample{},
	}

	profession = strings.TrimSpace(profession)
	if profession != "" {
		samples, err := p.professionCostSamples(ctx, profession, runType)
		if err != nil {
			if !isUndefinedTable(err) {
				return CostHistory{}, fmt.Errorf("api: profession cost samples: %w", err)
			}
			// LLM observability tables are not migrated: keep the run
			// durations, which live in runs.
			samples, err = p.professionRunDurations(ctx, profession, runType)
			if err != nil {
				return CostHistory{}, fmt.Errorf("api: profession run durations: %w", err)
			}
		}
		history.ProfessionRuns = samples
	}

	global, err := p.globalRunDurations(ctx, runType)
	if err != nil {
		return CostHistory{}, fmt.Errorf("api: global run durations: %w", err)
	}
	history.GlobalRuns = global

	stats, err := p.globalLLMRequestStats(ctx)
	if err != nil {
		if !isUndefinedTable(err) {
			return CostHistory{}, fmt.Errorf("api: global LLM request stats: %w", err)
		}
	} else {
		history.RequestStats = stats
	}
	return history, nil
}

// RunLLMFacts implements CostDataSource.
func (p *PGStore) RunLLMFacts(ctx context.Context, run *domain.Run) (RunLLMFacts, error) {
	if p == nil || p.Pool == nil {
		return RunLLMFacts{}, errors.New("api: run LLM facts require a PostgreSQL store")
	}
	if run == nil {
		return RunLLMFacts{}, errors.New("api: run is required")
	}
	ctx = ensureCostContext(ctx)
	empty := RunLLMFacts{Attribution: estimateAttributionNone, Requests: []LLMRequestFact{}}

	if validUUID(run.ID) {
		requests, err := p.llmRequestFactsByRunID(ctx, run.ID)
		if err != nil {
			if !isUndefinedTable(err) {
				return RunLLMFacts{}, fmt.Errorf("api: run LLM requests: %w", err)
			}
		} else if len(requests) > 0 {
			return RunLLMFacts{Attribution: estimateAttributionRunID, Requests: requests}, nil
		}
	}

	if run.StartedAt == nil {
		return empty, nil
	}
	windowStart := *run.StartedAt
	windowEnd := time.Now().UTC()
	if run.FinishedAt != nil {
		windowEnd = *run.FinishedAt
	}
	if !windowEnd.After(windowStart) {
		return empty, nil
	}

	ambiguous := false
	if validUUID(run.ID) {
		overlaps, err := p.runWindowOverlaps(ctx, run.ID, windowStart, windowEnd)
		if err != nil {
			return RunLLMFacts{}, fmt.Errorf("api: run window overlaps: %w", err)
		}
		ambiguous = overlaps
	}

	requests, err := p.llmRequestFactsInWindow(ctx, windowStart, windowEnd)
	if err != nil {
		if isUndefinedTable(err) {
			return empty, nil
		}
		return RunLLMFacts{}, fmt.Errorf("api: run window LLM requests: %w", err)
	}
	if len(requests) == 0 {
		return empty, nil
	}
	return RunLLMFacts{
		Attribution: estimateAttributionTimeWindow,
		Ambiguous:   ambiguous,
		Requests:    requests,
	}, nil
}

// professionCostSamples returns recent completed runs of one profession/type
// together with the usage attributed to each run. Usage is attributed by exact
// run_id first, then by the run window for runs that do not overlap any other
// run. Runs with overlapping windows keep their duration and report
// attribution="none" for usage, so the estimator can still use them as duration
// samples without double-counting tokens.
func (p *PGStore) professionCostSamples(ctx context.Context, profession string, runType domain.RunType) ([]CostRunSample, error) {
	const query = `WITH sample AS (
    SELECT id, started_at, finished_at
    FROM runs
    WHERE lower(btrim(profession)) = lower(btrim($1))
      AND type = $2
      AND status IN ('done', 'degraded')
      AND started_at IS NOT NULL
      AND finished_at IS NOT NULL
      AND finished_at > started_at
    ORDER BY finished_at DESC
    LIMIT $3
),
overlap_free AS (
    SELECT candidate.id, candidate.started_at, candidate.finished_at
    FROM sample candidate
    WHERE NOT EXISTS (
        SELECT 1
        FROM runs other
        WHERE other.id <> candidate.id
          AND other.started_at IS NOT NULL
          AND other.started_at < candidate.finished_at
          AND COALESCE(other.finished_at, now()) > candidate.started_at
    )
),
exact AS (
    SELECT req.run_id AS run_id,
           count(*)::bigint AS requests,
           count(usage.request_id)::bigint AS usage_rows,
           COALESCE(sum(usage.prompt_tokens), 0)::bigint AS prompt_tokens,
           COALESCE(sum(usage.completion_tokens), 0)::bigint AS completion_tokens,
           COALESCE(sum(usage.total_tokens), 0)::bigint AS total_tokens,
           count(usage.cost_usd)::bigint AS cost_rows,
           COALESCE(sum(usage.cost_usd), 0)::float8 AS cost_sum
    FROM llm_requests req
    LEFT JOIN llm_usage usage ON usage.request_id = req.id
    WHERE req.run_id IN (SELECT id FROM sample)
    GROUP BY req.run_id
),
windowed AS (
    SELECT free.id AS run_id,
           count(req.id)::bigint AS requests,
           count(usage.request_id)::bigint AS usage_rows,
           COALESCE(sum(usage.prompt_tokens), 0)::bigint AS prompt_tokens,
           COALESCE(sum(usage.completion_tokens), 0)::bigint AS completion_tokens,
           COALESCE(sum(usage.total_tokens), 0)::bigint AS total_tokens,
           count(usage.cost_usd)::bigint AS cost_rows,
           COALESCE(sum(usage.cost_usd), 0)::float8 AS cost_sum
    FROM overlap_free free
    LEFT JOIN llm_requests req
        ON req.run_id IS NULL
       AND req.created_at >= free.started_at
       AND req.created_at <= free.finished_at
    LEFT JOIN llm_usage usage ON usage.request_id = req.id
    GROUP BY free.id
)
SELECT sample.id::text,
       EXTRACT(EPOCH FROM (sample.finished_at - sample.started_at))::float8 AS duration_seconds,
       CASE
           WHEN exact.requests IS NOT NULL THEN 'run_id'
           WHEN windowed.requests IS NOT NULL AND windowed.requests > 0 THEN 'time_window'
           ELSE 'none'
       END AS attribution,
       COALESCE(exact.requests, windowed.requests, 0)::bigint AS requests,
       COALESCE(exact.usage_rows, windowed.usage_rows, 0)::bigint AS usage_rows,
       COALESCE(exact.prompt_tokens, windowed.prompt_tokens, 0)::bigint AS prompt_tokens,
       COALESCE(exact.completion_tokens, windowed.completion_tokens, 0)::bigint AS completion_tokens,
       COALESCE(exact.total_tokens, windowed.total_tokens, 0)::bigint AS total_tokens,
       CASE
           WHEN COALESCE(exact.cost_rows, windowed.cost_rows, 0) > 0
           THEN COALESCE(exact.cost_sum, windowed.cost_sum)
       END::numeric AS cost_usd
FROM sample
LEFT JOIN exact ON exact.run_id = sample.id
LEFT JOIN windowed ON windowed.run_id = sample.id
ORDER BY sample.finished_at DESC`

	rows, err := p.Pool.Query(ctx, query, profession, string(runType), estimateRunHistoryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CostRunSample{}
	for rows.Next() {
		var sample CostRunSample
		var requests, usageRows, promptTokens, completionTokens, totalTokens int64
		var cost pgtype.Numeric
		if err := rows.Scan(
			&sample.RunID, &sample.DurationSeconds, &sample.Attribution,
			&requests, &usageRows, &promptTokens, &completionTokens, &totalTokens,
			&cost,
		); err != nil {
			return nil, err
		}
		sample.RequestCount = int(requests)
		sample.UsageRows = int(usageRows)
		sample.PromptTokens = promptTokens
		sample.CompletionTokens = completionTokens
		sample.TotalTokens = totalTokens
		sample.CostUSD = costNumericPtr(cost)
		out = append(out, sample)
	}
	return out, rows.Err()
}

// professionRunDurations is the duration-only fallback used when the LLM
// observability tables are absent.
func (p *PGStore) professionRunDurations(ctx context.Context, profession string, runType domain.RunType) ([]CostRunSample, error) {
	const query = `SELECT id::text,
       EXTRACT(EPOCH FROM (finished_at - started_at))::float8
FROM runs
WHERE lower(btrim(profession)) = lower(btrim($1))
  AND type = $2
  AND status IN ('done', 'degraded')
  AND started_at IS NOT NULL
  AND finished_at IS NOT NULL
  AND finished_at > started_at
ORDER BY finished_at DESC
LIMIT $3`
	return p.scanRunDurations(ctx, query, profession, string(runType), estimateRunHistoryLimit)
}

// globalRunDurations returns the same-type completed runs of any profession,
// used to estimate the duration when the requested profession has no history.
func (p *PGStore) globalRunDurations(ctx context.Context, runType domain.RunType) ([]CostRunSample, error) {
	const query = `SELECT id::text,
       EXTRACT(EPOCH FROM (finished_at - started_at))::float8
FROM runs
WHERE type = $1
  AND status IN ('done', 'degraded')
  AND started_at IS NOT NULL
  AND finished_at IS NOT NULL
  AND finished_at > started_at
ORDER BY finished_at DESC
LIMIT $2`
	return p.scanRunDurations(ctx, query, string(runType), estimateRunHistoryLimit)
}

func (p *PGStore) scanRunDurations(ctx context.Context, query string, args ...any) ([]CostRunSample, error) {
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CostRunSample{}
	for rows.Next() {
		var sample CostRunSample
		if err := rows.Scan(&sample.RunID, &sample.DurationSeconds); err != nil {
			return nil, err
		}
		out = append(out, sample)
	}
	return out, rows.Err()
}

// globalLLMRequestStats aggregates all stored usage rows. It is the fallback
// denominator for the token estimate when no profession has attributed history.
func (p *PGStore) globalLLMRequestStats(ctx context.Context) (LLMRequestStats, error) {
	const query = `SELECT count(*)::bigint,
       COALESCE(sum(total_tokens), 0)::bigint,
       count(latency_ms)::bigint,
       COALESCE(sum(latency_ms), 0)::bigint,
       count(cost_usd)::bigint,
       COALESCE(sum(total_tokens) FILTER (WHERE cost_usd IS NOT NULL), 0)::bigint,
       COALESCE(sum(cost_usd), 0)::float8
FROM llm_usage`

	var stats LLMRequestStats
	err := p.Pool.QueryRow(ctx, query).Scan(
		&stats.RequestCount, &stats.TotalTokens,
		&stats.LatencyCount, &stats.TotalLatencyMS,
		&stats.CostRequestCount, &stats.CostTokens, &stats.CostSumUSD,
	)
	if err != nil {
		return LLMRequestStats{}, err
	}
	return stats, nil
}

// llmRequestFactsByRunID returns the exactly attributed requests of one run.
func (p *PGStore) llmRequestFactsByRunID(ctx context.Context, runID string) ([]LLMRequestFact, error) {
	query := llmRequestFactSelect + `WHERE req.run_id = $1::uuid
ORDER BY req.created_at, req.id`
	return p.scanLLMRequestFacts(ctx, query, runID)
}

// llmRequestFactsInWindow returns requests without a run id that were created
// inside the run window. It is the fallback attribution.
func (p *PGStore) llmRequestFactsInWindow(ctx context.Context, startedAt, finishedAt time.Time) ([]LLMRequestFact, error) {
	query := llmRequestFactSelect + `WHERE req.run_id IS NULL
  AND req.created_at >= $1
  AND req.created_at <= $2
ORDER BY req.created_at, req.id`
	return p.scanLLMRequestFacts(ctx, query, startedAt, finishedAt)
}

func (p *PGStore) scanLLMRequestFacts(ctx context.Context, query string, args ...any) ([]LLMRequestFact, error) {
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LLMRequestFact{}
	for rows.Next() {
		var fact LLMRequestFact
		var cost pgtype.Numeric
		if err := rows.Scan(
			&fact.Agent, &fact.Model, &fact.Status,
			&fact.PromptTokens, &fact.CompletionTokens, &fact.TotalTokens,
			&fact.LatencyMS, &cost,
		); err != nil {
			return nil, err
		}
		fact.CostUSD = costNumericPtr(cost)
		out = append(out, fact)
	}
	return out, rows.Err()
}

// runWindowOverlaps reports whether another run's window intersects
// [windowStart, windowEnd]. A still-running run counts as ending now.
func (p *PGStore) runWindowOverlaps(ctx context.Context, runID string, windowStart, windowEnd time.Time) (bool, error) {
	const query = `SELECT EXISTS (
    SELECT 1
    FROM runs other
    WHERE other.id <> $1::uuid
      AND other.started_at IS NOT NULL
      AND other.started_at < $2
      AND COALESCE(other.finished_at, now()) > $3
)`
	var overlaps bool
	if err := p.Pool.QueryRow(ctx, query, runID, windowEnd, windowStart).Scan(&overlaps); err != nil {
		return false, err
	}
	return overlaps, nil
}

// ensureCostContext protects the raw pgx calls from a nil context (handlers
// always pass the request context).
func ensureCostContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// costNumericPtr converts a nullable NUMERIC cost into a *float64. nil means
// "the provider did not report a cost", which is different from zero.
func costNumericPtr(value pgtype.Numeric) *float64 {
	if !value.Valid {
		return nil
	}
	parsed, err := value.Float64Value()
	if err != nil || !parsed.Valid {
		return nil
	}
	cost := parsed.Float64
	return &cost
}

// isUndefinedTable reports PostgreSQL error 42P01 (undefined_table). The API
// keeps working without the optional LLM observability tables: estimates then
// use run durations only and the run cost endpoint reports no LLM data.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// compile-time check that the PostgreSQL store implements the cost data source.
var _ CostDataSource = (*PGStore)(nil)
