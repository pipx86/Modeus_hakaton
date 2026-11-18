package api

// Cost and time estimate for a future run (mentor feature #12).
//
// The endpoints are read-only and never call an external provider: the estimate
// is built only from data already stored by this backend.
//
//	GET /api/v1/runs/estimate?profession=<name>&type=chat|batch
//	GET /api/v1/runs/{id}/cost
//
// # Estimate formula
//
// Duration: median wall-clock duration of completed runs (status done/degraded)
// for the same profession (case-insensitive, trimmed) and the same run type.
// When that sample is empty, the median of all completed runs of the same type
// is used; when there is no completed run at all, a documented constant is
// returned. The median keeps one hung or retried run from skewing the estimate.
//
// Tokens: mean total tokens per completed run of the same profession/type, but
// only over runs whose LLM usage is attributed to them (see below). When no
// such run exists, the estimate is the global mean tokens of one stored LLM
// request multiplied by the expected number of provider calls of the run type
// (batch: 1 analyst call; chat: 6 planned interview questions + 1 analyst
// finalize). Only when the llm_usage table is empty are the constants from
// docs/LIVE_EVAL.md and docs/LLM_MODEL_BAKEOFF.md used.
//
// Cost USD: mean recorded cost_usd of the same-profession runs, then of all
// runs; then a price per token derived from stored llm_usage cost rows; then
// the optional COST_ESTIMATE_USD_PER_1M_TOKENS fallback price; otherwise null
// (cost is unknown, not zero). llm_usage.cost_usd is only written when the
// provider reports a price, so "null" is the honest answer for most setups.
//
// Confidence is a three-state label derived from sample sizes, never a decimal:
//
//	high   - at least 10 duration samples and at least 10 token samples;
//	medium - at least 3 duration samples and at least 3 token samples;
//	low    - anything smaller, any default-based input, or no history at all.
//
// SampleSize is the number of completed runs behind the duration estimate.
// Method names which inputs were used: "profession_history" (duration and
// tokens from same-profession runs), "partial_history" (one of them from the
// profession, the other from global data), "global_history" (duration from all
// runs of the type or tokens from global request statistics), "defaults".
//
// # Attribution of LLM requests to a run
//
// llm_requests.run_id is the exact attribution: the recorder stores the run id
// from the call context (llmobs.WithRunID, propagated by the chat
// interview/finalizer and the batch analyst step). Rows created before that
// wiring, or by binaries without the recorder, still have run_id NULL; for them
// a run's window [started_at, finished_at] is used as a fallback. Two sources
// are reported explicitly in the /runs/{id}/cost response:
//
//	"run_id"      - exact rows, no guessing;
//	"time_window" - rows created during the run window; if another run's window
//	                overlaps, attribution_ambiguous is true and the numbers are
//	                approximate;
//	"none"        - no attributable LLM data.
//
// The historical sample used by the estimator drops runs whose window overlaps
// another run, so overlapping requests are never counted twice and no metric is
// invented.

import (
	"context"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/logging"
)

const (
	// estimateRunHistoryLimit bounds how many completed runs one estimate reads.
	// A recent sample is both cheaper and more representative than all history.
	estimateRunHistoryLimit = 20

	// expectedBatchLLMCalls is the number of provider calls of one batch run:
	// the interviewer step in a batch is a no-op, the synthesizer is
	// deterministic and only the analyst calls the model (see steps.AISteps).
	expectedBatchLLMCalls = 1
	// expectedChatLLMCalls is 6 planned interview questions
	// (interviewer.defaultPlan) plus one analyst finalize call. The live eval
	// observed exactly 7 model calls for a chat-like baseline run
	// (docs/LIVE_EVAL.md).
	expectedChatLLMCalls = 7

	// Last-resort constants, used only when no history exists at all. They are
	// grounded in observed live runs, not in precise accounting:
	// a chat run made 7 calls in ~36-67 s, one analyst call takes ~20-28 s and
	// consumes ~2.4k-6.1k tokens (docs/LIVE_EVAL.md,
	// docs/LLM_MODEL_BAKEOFF.md). The estimates are deliberately coarse.
	defaultBatchTokenEstimate      = 4000
	defaultChatTokenEstimate       = 10000
	defaultBatchDurationSeconds    = 60
	defaultChatDurationSeconds     = 120
	estimateCostPriceEnvVariable   = "COST_ESTIMATE_USD_PER_1M_TOKENS"
	estimateConfidenceHigh         = "high"
	estimateConfidenceMedium       = "medium"
	estimateConfidenceLow          = "low"
	estimateMethodProfession       = "profession_history"
	estimateMethodPartial          = "partial_history"
	estimateMethodGlobal           = "global_history"
	estimateMethodDefaults         = "defaults"
	estimateAttributionRunID       = "run_id"
	estimateAttributionTimeWindow  = "time_window"
	estimateAttributionNone        = "none"
	durationSourceProfession       = "profession"
	durationSourceGlobal           = "global"
	durationSourceDefault          = "default"
	tokenSourceProfession          = "profession"
	tokenSourceGlobalRequests      = "global_requests"
	tokenSourceDefault             = "default"
	estimateCostRoundingDecimals   = 6
	estimateConfidenceSampleHigh   = 10
	estimateConfidenceSampleMedium = 3
)

// CostRunSample is one completed run and the LLM usage attributed to it. It is
// read by CostDataSource implementations.
type CostRunSample struct {
	RunID string
	// DurationSeconds is finished_at - started_at; always positive for samples.
	DurationSeconds float64
	// RequestCount is the number of LLM requests attributed to the run.
	RequestCount int
	// UsageRows is the number of attributed requests that carry an llm_usage
	// row. Only runs with at least one usage row contribute a token sample.
	UsageRows        int
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	// CostUSD is nil when no attributed request carries a recorded cost.
	CostUSD *float64
	// Attribution is "run_id", "time_window" or "none".
	Attribution string
}

// LLMRequestStats aggregates every stored llm_usage row. It is the global
// fallback for professions without attributed history.
type LLMRequestStats struct {
	// RequestCount is the number of llm_usage rows (successful calls).
	RequestCount     int64
	TotalTokens      int64
	LatencyCount     int64
	TotalLatencyMS   int64
	CostRequestCount int64
	// CostTokens is the token total of the rows that have a recorded cost, so
	// CostSumUSD/CostTokens is a token price derived from real usage.
	CostTokens int64
	CostSumUSD float64
}

// CostHistory is the estimator input read from a CostDataSource.
type CostHistory struct {
	// ProfessionRuns are recent completed runs of the requested profession and
	// type. Runs whose window overlaps another run have no attributed usage.
	ProfessionRuns []CostRunSample
	// GlobalRuns are recent completed runs of the same type for any profession.
	GlobalRuns   []CostRunSample
	RequestStats LLMRequestStats
}

// LLMRequestFact is one LLM request attributed to a run.
type LLMRequestFact struct {
	Agent            string
	Model            string
	Status           string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMS        int
	// CostUSD is nil when the provider did not report a cost.
	CostUSD *float64
}

// RunLLMFacts is the LLM usage attributed to one run by a CostDataSource.
type RunLLMFacts struct {
	// Attribution is "run_id", "time_window" or "none".
	Attribution string
	// Ambiguous is true when the run window overlaps another run, so the
	// time_window attribution may include requests of a parallel run.
	Ambiguous bool
	Requests  []LLMRequestFact
}

// CostDataSource is the optional storage capability behind the estimate and
// cost endpoints. *PGStore implements it; other Store implementations (the
// in-memory store used by tests and the demo server) simply do not, and the
// handlers fall back to defaults / empty facts.
type CostDataSource interface {
	// CostHistory returns recent historical samples for profession and type.
	CostHistory(ctx context.Context, profession string, runType domain.RunType) (CostHistory, error)
	// RunLLMFacts returns the LLM usage attributed to one run.
	RunLLMFacts(ctx context.Context, run *domain.Run) (RunLLMFacts, error)
}

// RunEstimateResponse is the JSON body of GET /api/v1/runs/estimate.
type RunEstimateResponse struct {
	Profession               string         `json:"profession"`
	Type                     domain.RunType `json:"type"`
	SampleSize               int            `json:"sample_size"`
	EstimatedTokens          int            `json:"estimated_tokens"`
	EstimatedDurationSeconds int            `json:"estimated_duration_seconds"`
	// EstimatedCostUSD is null when no cost can be estimated honestly.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`
	Confidence       string   `json:"confidence"`
	Method           string   `json:"method"`
}

// ModelUsageSummary is token/cost usage of one model inside one run.
type ModelUsageSummary struct {
	Model            string   `json:"model"`
	RequestCount     int      `json:"request_count"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
}

// AgentUsageSummary is token usage of one agent inside one run.
type AgentUsageSummary struct {
	Agent            string   `json:"agent"`
	RequestCount     int      `json:"request_count"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
}

// StatusCount is one bucket of the request status breakdown.
type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// RunCostResponse is the JSON body of GET /api/v1/runs/{id}/cost. Actual facts
// (tokens, cost, duration, request count, status breakdown) are reported next
// to the estimate recomputed from the same profession/type history, so the
// client can show the deviation without inventing anything.
type RunCostResponse struct {
	RunID      string           `json:"run_id"`
	Profession string           `json:"profession"`
	Type       domain.RunType   `json:"type"`
	Status     domain.RunStatus `json:"status"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	// DurationSeconds is the finished run duration, or the elapsed time of a
	// running run measured at request time; null when the run never started.
	DurationSeconds  *int                `json:"duration_seconds"`
	RequestCount     int                 `json:"request_count"`
	PromptTokens     int                 `json:"prompt_tokens"`
	CompletionTokens int                 `json:"completion_tokens"`
	TotalTokens      int                 `json:"total_tokens"`
	CostUSD          *float64            `json:"cost_usd"`
	ByModel          []ModelUsageSummary `json:"by_model"`
	ByAgent          []AgentUsageSummary `json:"by_agent"`
	StatusCounts     []StatusCount       `json:"status_counts"`
	Attribution      string              `json:"attribution"`
	// AttributionAmbiguous is true when the time_window attribution may include
	// requests of a parallel run.
	AttributionAmbiguous bool                `json:"attribution_ambiguous"`
	Estimate             RunEstimateResponse `json:"estimate"`
}

// handleRunEstimate implements GET /api/v1/runs/estimate.
//
// Errors: 400 for a missing profession or an unknown type; 401/403 from the
// auth middleware; 500 when the storage layer fails (the estimate is never
// silently replaced by defaults on a storage error, because that would hide an
// outage).
func (s *Server) handleRunEstimate(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP) {
		return
	}

	profession := strings.TrimSpace(r.URL.Query().Get("profession"))
	if profession == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "profession is required")
		return
	}
	runType, ok := parseEstimateRunType(r.URL.Query().Get("type"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "type must be chat or batch")
		return
	}

	history := CostHistory{}
	if source, ok := s.store.(CostDataSource); ok {
		loaded, err := source.CostHistory(r.Context(), profession, runType)
		if err != nil {
			logging.Logger.Error("run_estimate_history_failed",
				"profession", profession, "type", string(runType), "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "failed to read run history")
			return
		}
		history = loaded
	}

	writeJSON(w, http.StatusOK, buildRunEstimate(profession, runType, history, estimateCostPerMillionTokens()))
}

// handleRunCost implements GET /api/v1/runs/{id}/cost.
//
// The read roles match GET /api/v1/runs/{id}: admin, teacher, rop, student and
// guest may read a run, so they may read its cost as well.
//
// Errors: 404 for an unknown run; 500 when the run or its usage cannot be read.
func (s *Server) handleRunCost(w http.ResponseWriter, r *http.Request) {
	if !auth.RequireRole(w, r, auth.RoleAdmin, auth.RoleTeacher, auth.RoleROP, auth.RoleStudent, auth.RoleGuest) {
		return
	}
	runID := r.PathValue("id")

	run, err := s.store.GetRun(runID)
	if err != nil {
		if mapStoreError(w, err) {
			return
		}
		logging.Logger.Error("run_cost_get_run_failed", "run_id", runID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "failed to load run")
		return
	}

	facts := RunLLMFacts{Attribution: estimateAttributionNone}
	history := CostHistory{}
	if source, ok := s.store.(CostDataSource); ok {
		loadedFacts, factsErr := source.RunLLMFacts(r.Context(), run)
		if factsErr != nil {
			logging.Logger.Error("run_cost_facts_failed", "run_id", runID, "error", factsErr)
			writeErr(w, http.StatusInternalServerError, "INTERNAL", "failed to read run usage")
			return
		}
		facts = loadedFacts
		// The estimate is auxiliary: an estimate failure must not hide the
		// actual facts, so it degrades to the defaults-based estimate.
		loadedHistory, historyErr := source.CostHistory(r.Context(), run.Profession, effectiveRunType(run))
		if historyErr != nil {
			logging.Logger.Error("run_cost_estimate_history_failed", "run_id", runID, "error", historyErr)
		} else {
			history = loadedHistory
		}
	}

	history = historyWithoutRun(history, run.ID)
	writeJSON(w, http.StatusOK, buildRunCostResponse(run, facts, history, estimateCostPerMillionTokens(), time.Now().UTC()))
}

// parseEstimateRunType validates the ?type= query value. Empty is invalid: the
// pre-run estimate always names the run type it is for.
func parseEstimateRunType(raw string) (domain.RunType, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(domain.RunTypeChat):
		return domain.RunTypeChat, true
	case string(domain.RunTypeBatch):
		return domain.RunTypeBatch, true
	default:
		return "", false
	}
}

// effectiveRunType normalizes the stored run type. Rows created before the
// type column existed are batch runs (the historical default).
func effectiveRunType(run *domain.Run) domain.RunType {
	if run != nil && run.Type == domain.RunTypeChat {
		return domain.RunTypeChat
	}
	return domain.RunTypeBatch
}

// estimateCostPerMillionTokens reads the optional fallback token price. A
// missing, invalid or negative value means "no configured price" and the cost
// estimate stays null unless history provides one.
func estimateCostPerMillionTokens() float64 {
	raw := strings.TrimSpace(os.Getenv(estimateCostPriceEnvVariable))
	if raw == "" {
		return 0
	}
	price, err := strconv.ParseFloat(raw, 64)
	if err != nil || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0
	}
	return price
}

// historyWithoutRun removes one run from the history. The cost endpoint uses it
// so the actual run does not bias the estimate it is compared against.
func historyWithoutRun(history CostHistory, runID string) CostHistory {
	history.ProfessionRuns = samplesWithoutRun(history.ProfessionRuns, runID)
	history.GlobalRuns = samplesWithoutRun(history.GlobalRuns, runID)
	return history
}

func samplesWithoutRun(samples []CostRunSample, runID string) []CostRunSample {
	if runID == "" {
		return samples
	}
	out := samples[:0]
	for _, sample := range samples {
		if sample.RunID != runID {
			out = append(out, sample)
		}
	}
	return out
}

// buildRunEstimate applies the formula documented at the top of the file. It is
// pure and deterministic: no clock, no network, no randomness.
func buildRunEstimate(profession string, runType domain.RunType, history CostHistory, costPerMillionTokens float64) RunEstimateResponse {
	runType = normalizeEstimateRunType(runType)
	response := RunEstimateResponse{Profession: profession, Type: runType}

	durationSource, durationSeconds, durationSamples := estimateDuration(runType, history)
	tokenSource, tokenEstimate, tokenSamples := estimateTokens(runType, history)

	response.SampleSize = durationSamples
	response.EstimatedDurationSeconds = positiveRoundedInt(durationSeconds)
	response.EstimatedTokens = nonNegativeRoundedInt(tokenEstimate)

	response.EstimatedCostUSD = estimateCostUSD(history, response.EstimatedTokens, costPerMillionTokens)
	response.Method = estimateMethod(durationSource, tokenSource)
	response.Confidence = estimateConfidence(durationSource, durationSamples, tokenSource, tokenSamples)
	return response
}

func normalizeEstimateRunType(runType domain.RunType) domain.RunType {
	if runType == domain.RunTypeChat {
		return domain.RunTypeChat
	}
	return domain.RunTypeBatch
}

// estimateDuration returns (source, seconds, sample count).
func estimateDuration(runType domain.RunType, history CostHistory) (string, float64, int) {
	if durations := validDurations(history.ProfessionRuns); len(durations) > 0 {
		return durationSourceProfession, median(durations), len(durations)
	}
	if durations := validDurations(history.GlobalRuns); len(durations) > 0 {
		return durationSourceGlobal, median(durations), len(durations)
	}
	if runType == domain.RunTypeChat {
		return durationSourceDefault, defaultChatDurationSeconds, 0
	}
	return durationSourceDefault, defaultBatchDurationSeconds, 0
}

// estimateTokens returns (source, tokens, sample count).
func estimateTokens(runType domain.RunType, history CostHistory) (string, float64, int) {
	var tokenSamples []float64
	for _, sample := range history.ProfessionRuns {
		if sample.UsageRows <= 0 {
			continue
		}
		if sample.TotalTokens < 0 {
			continue
		}
		tokenSamples = append(tokenSamples, float64(sample.TotalTokens))
	}
	if len(tokenSamples) > 0 {
		return tokenSourceProfession, mean(tokenSamples), len(tokenSamples)
	}
	if stats := history.RequestStats; stats.RequestCount > 0 && stats.TotalTokens >= 0 {
		perCall := float64(stats.TotalTokens) / float64(stats.RequestCount)
		return tokenSourceGlobalRequests, perCall * float64(expectedLLMCalls(runType)), int(stats.RequestCount)
	}
	if runType == domain.RunTypeChat {
		return tokenSourceDefault, defaultChatTokenEstimate, 0
	}
	return tokenSourceDefault, defaultBatchTokenEstimate, 0
}

// estimateCostUSD follows the cost order documented at the top of the file.
func estimateCostUSD(history CostHistory, estimatedTokens int, costPerMillionTokens float64) *float64 {
	if value, ok := meanRecordedCost(history.ProfessionRuns); ok {
		return roundedCost(value)
	}
	if value, ok := meanRecordedCost(history.GlobalRuns); ok {
		return roundedCost(value)
	}
	stats := history.RequestStats
	if stats.CostRequestCount > 0 && stats.CostTokens > 0 && stats.CostSumUSD >= 0 {
		pricePerToken := stats.CostSumUSD / float64(stats.CostTokens)
		return roundedCost(pricePerToken * float64(estimatedTokens))
	}
	if costPerMillionTokens > 0 {
		return roundedCost(float64(estimatedTokens) / 1_000_000 * costPerMillionTokens)
	}
	return nil
}

func estimateMethod(durationSource, tokenSource string) string {
	switch {
	case durationSource == durationSourceProfession && tokenSource == tokenSourceProfession:
		return estimateMethodProfession
	case durationSource == durationSourceProfession || tokenSource == tokenSourceProfession:
		return estimateMethodPartial
	case durationSource == durationSourceGlobal || tokenSource == tokenSourceGlobalRequests:
		return estimateMethodGlobal
	default:
		return estimateMethodDefaults
	}
}

func estimateConfidence(durationSource string, durationSamples int, tokenSource string, tokenSamples int) string {
	durationLevel := sampleLevel(durationSamples)
	if durationSource == durationSourceDefault {
		durationLevel = 0
	}
	tokenLevel := sampleLevel(tokenSamples)
	if tokenSource == tokenSourceDefault {
		tokenLevel = 0
	}
	return confidenceName(min(durationLevel, tokenLevel))
}

// sampleLevel maps a sample size to a confidence level: 2 high, 1 medium,
// 0 low. The thresholds are the documented ones (10 and 3).
func sampleLevel(samples int) int {
	switch {
	case samples >= estimateConfidenceSampleHigh:
		return 2
	case samples >= estimateConfidenceSampleMedium:
		return 1
	default:
		return 0
	}
}

func confidenceName(level int) string {
	switch level {
	case 2:
		return estimateConfidenceHigh
	case 1:
		return estimateConfidenceMedium
	default:
		return estimateConfidenceLow
	}
}

func expectedLLMCalls(runType domain.RunType) int {
	if runType == domain.RunTypeChat {
		return expectedChatLLMCalls
	}
	return expectedBatchLLMCalls
}

func validDurations(samples []CostRunSample) []float64 {
	out := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.DurationSeconds > 0 && !math.IsNaN(sample.DurationSeconds) && !math.IsInf(sample.DurationSeconds, 0) {
			out = append(out, sample.DurationSeconds)
		}
	}
	return out
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func meanRecordedCost(samples []CostRunSample) (float64, bool) {
	var values []float64
	for _, sample := range samples {
		if sample.CostUSD == nil {
			continue
		}
		if *sample.CostUSD < 0 || math.IsNaN(*sample.CostUSD) || math.IsInf(*sample.CostUSD, 0) {
			continue
		}
		values = append(values, *sample.CostUSD)
	}
	if len(values) == 0 {
		return 0, false
	}
	return mean(values), true
}

func roundedCost(value float64) *float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	factor := math.Pow10(estimateCostRoundingDecimals)
	rounded := math.Round(value*factor) / factor
	return &rounded
}

func positiveRoundedInt(value float64) int {
	if math.IsNaN(value) || value < 1 {
		return 1
	}
	rounded := int(math.Round(value))
	if rounded < 1 {
		return 1
	}
	return rounded
}

func nonNegativeRoundedInt(value float64) int {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	return int(math.Round(value))
}

// buildRunCostResponse aggregates the attributed requests and adds the
// estimate. It is pure apart from the injected now (used only for the elapsed
// duration of a running run), which keeps it directly testable.
func buildRunCostResponse(run *domain.Run, facts RunLLMFacts, history CostHistory, costPerMillionTokens float64, now time.Time) RunCostResponse {
	runType := effectiveRunType(run)
	response := RunCostResponse{
		RunID:                run.ID,
		Profession:           run.Profession,
		Type:                 runType,
		Status:               run.Status,
		StartedAt:            run.StartedAt,
		FinishedAt:           run.FinishedAt,
		DurationSeconds:      runDurationSeconds(run, now),
		ByModel:              []ModelUsageSummary{},
		ByAgent:              []AgentUsageSummary{},
		StatusCounts:         []StatusCount{},
		Attribution:          facts.Attribution,
		AttributionAmbiguous: facts.Ambiguous,
		Estimate:             buildRunEstimate(run.Profession, runType, history, costPerMillionTokens),
	}
	if response.Attribution == "" {
		response.Attribution = estimateAttributionNone
	}

	byModel := map[string]*usageTotals{}
	byAgent := map[string]*usageTotals{}
	statusCounts := map[string]int{}
	for _, request := range facts.Requests {
		response.RequestCount++
		response.PromptTokens += request.PromptTokens
		response.CompletionTokens += request.CompletionTokens
		response.TotalTokens += request.TotalTokens

		if byModel[request.Model] == nil {
			byModel[request.Model] = &usageTotals{}
		}
		byModel[request.Model].add(request)
		if byAgent[request.Agent] == nil {
			byAgent[request.Agent] = &usageTotals{}
		}
		byAgent[request.Agent].add(request)
		statusCounts[request.Status]++
	}
	response.CostUSD = sumKnownCosts(facts.Requests)
	response.ByModel = sortedModelUsage(byModel)
	response.ByAgent = sortedAgentUsage(byAgent)
	response.StatusCounts = sortedStatusCounts(statusCounts)
	return response
}

// usageTotals is the running aggregate of one model or agent group.
type usageTotals struct {
	requests         int
	promptTokens     int
	completionTokens int
	totalTokens      int
	costSum          float64
	costKnown        bool
}

func (t *usageTotals) add(request LLMRequestFact) {
	t.requests++
	t.promptTokens += request.PromptTokens
	t.completionTokens += request.CompletionTokens
	t.totalTokens += request.TotalTokens
	if request.CostUSD != nil && *request.CostUSD >= 0 {
		t.costSum += *request.CostUSD
		t.costKnown = true
	}
}

func (t *usageTotals) cost() *float64 {
	if !t.costKnown {
		return nil
	}
	return roundedCost(t.costSum)
}

// sumKnownCosts returns the sum of recorded costs, or nil when no request has
// a recorded cost. A nil value means "unknown", never "free".
func sumKnownCosts(requests []LLMRequestFact) *float64 {
	total := 0.0
	known := false
	for _, request := range requests {
		if request.CostUSD == nil || *request.CostUSD < 0 {
			continue
		}
		total += *request.CostUSD
		known = true
	}
	if !known {
		return nil
	}
	return roundedCost(total)
}

func sortedModelUsage(groups map[string]*usageTotals) []ModelUsageSummary {
	out := make([]ModelUsageSummary, 0, len(groups))
	for model, totals := range groups {
		out = append(out, ModelUsageSummary{
			Model:            model,
			RequestCount:     totals.requests,
			PromptTokens:     totals.promptTokens,
			CompletionTokens: totals.completionTokens,
			TotalTokens:      totals.totalTokens,
			CostUSD:          totals.cost(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func sortedAgentUsage(groups map[string]*usageTotals) []AgentUsageSummary {
	out := make([]AgentUsageSummary, 0, len(groups))
	for agent, totals := range groups {
		out = append(out, AgentUsageSummary{
			Agent:            agent,
			RequestCount:     totals.requests,
			PromptTokens:     totals.promptTokens,
			CompletionTokens: totals.completionTokens,
			TotalTokens:      totals.totalTokens,
			CostUSD:          totals.cost(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

func sortedStatusCounts(counts map[string]int) []StatusCount {
	out := make([]StatusCount, 0, len(counts))
	for status, count := range counts {
		out = append(out, StatusCount{Status: status, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Status < out[j].Status })
	return out
}

// runDurationSeconds returns the finished duration, the elapsed time of a
// running run, or nil when the run has not started.
func runDurationSeconds(run *domain.Run, now time.Time) *int {
	if run == nil || run.StartedAt == nil {
		return nil
	}
	end := now
	if run.FinishedAt != nil {
		end = *run.FinishedAt
	}
	elapsed := end.Sub(*run.StartedAt)
	if elapsed < 0 {
		return nil
	}
	seconds := int(elapsed / time.Second)
	return &seconds
}
