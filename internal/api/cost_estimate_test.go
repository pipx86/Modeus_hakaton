package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"
)

// costStubStore is a Store test double with the optional CostDataSource
// capability. The embedded Store provides the in-memory run storage; the two
// cost methods return canned data and record their arguments.
type costStubStore struct {
	Store
	history        CostHistory
	historyErr     error
	facts          RunLLMFacts
	factsErr       error
	getRunErr      error
	historyCalls   int
	lastProfession string
	lastType       domain.RunType
	factsRunID     string
}

func (s *costStubStore) GetRun(id string) (*domain.Run, error) {
	if s.getRunErr != nil {
		return nil, s.getRunErr
	}
	return s.Store.GetRun(id)
}

func (s *costStubStore) CostHistory(_ context.Context, profession string, runType domain.RunType) (CostHistory, error) {
	s.historyCalls++
	s.lastProfession = profession
	s.lastType = runType
	if s.historyErr != nil {
		return CostHistory{}, s.historyErr
	}
	return s.history, nil
}

func (s *costStubStore) RunLLMFacts(_ context.Context, run *domain.Run) (RunLLMFacts, error) {
	s.factsRunID = run.ID
	if s.factsErr != nil {
		return RunLLMFacts{}, s.factsErr
	}
	return s.facts, nil
}

// costTestRouter mounts only the two cost routes and the real auth middleware.
// The production routes are wired by the lead in handlers.go; these tests use
// the same patterns so auth (401/403) and path values are exercised exactly as
// the final router will exercise them.
func costTestRouter(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs/estimate", s.handleRunEstimate)
	mux.HandleFunc("GET /api/v1/runs/{id}/cost", s.handleRunCost)
	return auth.Middleware(mux)
}

func costDo(t *testing.T, handler http.Handler, method, target, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if role != "" {
		req.Header.Set("Authorization", "Bearer "+auth.TokenFor("cost-test-subject", auth.Role(role)))
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeEstimateBody(t *testing.T, recorder *httptest.ResponseRecorder) RunEstimateResponse {
	t.Helper()
	var body RunEstimateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("estimate JSON: %v (%s)", err, recorder.Body.String())
	}
	return body
}

func decodeCostBody(t *testing.T, recorder *httptest.ResponseRecorder) RunCostResponse {
	t.Helper()
	var body RunCostResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("cost JSON: %v (%s)", err, recorder.Body.String())
	}
	return body
}

func seededRun(id, profession string, runType domain.RunType, status domain.RunStatus, started, finished time.Time) domain.Run {
	run := domain.Run{ID: id, Profession: profession, Type: runType, Status: status}
	if !started.IsZero() {
		run.StartedAt = &started
	}
	if !finished.IsZero() {
		run.FinishedAt = &finished
	}
	return run
}

func TestRunEstimateDefaultsAndValidation(t *testing.T) {
	server := NewServer(NewMemStoreForTest())
	handler := costTestRouter(server)

	for _, tc := range []struct {
		profession string
		runType    string
		wantTokens int
		wantDur    int
	}{
		{"Повар", "chat", defaultChatTokenEstimate, defaultChatDurationSeconds},
		{"Повар", "batch", defaultBatchTokenEstimate, defaultBatchDurationSeconds},
		{"Повар", "CHAT", defaultChatTokenEstimate, defaultChatDurationSeconds},
	} {
		target := "/api/v1/runs/estimate?profession=" + url.QueryEscape(" "+tc.profession+" ") + "&type=" + tc.runType
		recorder := costDo(t, handler, http.MethodGet, target, "admin")
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (%s)", tc.runType, recorder.Code, recorder.Body.String())
		}
		body := decodeEstimateBody(t, recorder)
		if body.Profession != tc.profession {
			t.Fatalf("profession = %q, want %q", body.Profession, tc.profession)
		}
		if body.SampleSize != 0 || body.Method != estimateMethodDefaults || body.Confidence != estimateConfidenceLow {
			t.Fatalf("defaults body = %+v", body)
		}
		if body.EstimatedTokens != tc.wantTokens || body.EstimatedDurationSeconds != tc.wantDur {
			t.Fatalf("defaults body = %+v, want tokens %d dur %d", body, tc.wantTokens, tc.wantDur)
		}
		if body.EstimatedCostUSD != nil {
			t.Fatalf("cost without history = %v, want null", *body.EstimatedCostUSD)
		}
		if !strings.Contains(recorder.Body.String(), `"estimated_cost_usd":null`) {
			t.Fatalf("cost must serialize as explicit null: %s", recorder.Body.String())
		}
	}

	for _, target := range []string{
		"/api/v1/runs/estimate?type=chat",
		"/api/v1/runs/estimate?profession=%20%20&type=chat",
		"/api/v1/runs/estimate?profession=cook",
		"/api/v1/runs/estimate?profession=cook&type=video",
	} {
		if recorder := costDo(t, handler, http.MethodGet, target, "admin"); recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (%s)", target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRunEstimateAuth(t *testing.T) {
	server := NewServer(NewMemStoreForTest())
	handler := costTestRouter(server)
	const target = "/api/v1/runs/estimate?profession=cook&type=chat"

	if recorder := costDo(t, handler, http.MethodGet, target, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", recorder.Code)
	}
	for _, role := range []string{"student", "guest"} {
		if recorder := costDo(t, handler, http.MethodGet, target, role); recorder.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", role, recorder.Code)
		}
	}
	for _, role := range []string{"admin", "teacher", "rop"} {
		if recorder := costDo(t, handler, http.MethodGet, target, role); recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", role, recorder.Code)
		}
	}
}

func TestRunEstimateProfessionHistory(t *testing.T) {
	runStore := newMemStore()
	runStore.seedRun(seededRun("run-1", "Повар", domain.RunTypeChat, domain.RunDone, time.Time{}, time.Time{}))
	stub := &costStubStore{
		Store: runStore,
		history: CostHistory{
			ProfessionRuns: []CostRunSample{
				{RunID: "r1", DurationSeconds: 60, RequestCount: 7, UsageRows: 7, TotalTokens: 3000},
				{RunID: "r2", DurationSeconds: 90, RequestCount: 7, UsageRows: 7, TotalTokens: 3000},
				{RunID: "r3", DurationSeconds: 120, RequestCount: 7, UsageRows: 7, TotalTokens: 4000},
				{RunID: "r4", DurationSeconds: 150, RequestCount: 7, UsageRows: 7, TotalTokens: 4000},
			},
			GlobalRuns: []CostRunSample{{RunID: "g1", DurationSeconds: 15}},
		},
	}
	handler := costTestRouter(NewServer(stub))

	recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/estimate?profession="+url.QueryEscape(" Повар ")+"&type=chat", "teacher")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	body := decodeEstimateBody(t, recorder)
	if body.Profession != "Повар" || body.Type != domain.RunTypeChat {
		t.Fatalf("identity = %+v", body)
	}
	if stub.lastProfession != "Повар" || stub.lastType != domain.RunTypeChat {
		t.Fatalf("provider got profession %q type %q", stub.lastProfession, stub.lastType)
	}
	if body.SampleSize != 4 || body.EstimatedDurationSeconds != 105 {
		t.Fatalf("sample/duration = %d/%d, want 4/105", body.SampleSize, body.EstimatedDurationSeconds)
	}
	if body.EstimatedTokens != 3500 {
		t.Fatalf("tokens = %d, want 3500", body.EstimatedTokens)
	}
	if body.Method != estimateMethodProfession || body.Confidence != estimateConfidenceMedium {
		t.Fatalf("method/confidence = %q/%q", body.Method, body.Confidence)
	}
}

func TestRunEstimateGlobalFallback(t *testing.T) {
	runStore := newMemStore()
	stub := &costStubStore{
		Store: runStore,
		history: CostHistory{
			GlobalRuns: []CostRunSample{
				{RunID: "g1", DurationSeconds: 30},
				{RunID: "g2", DurationSeconds: 60},
				{RunID: "g3", DurationSeconds: 90},
			},
			RequestStats: LLMRequestStats{RequestCount: 100, TotalTokens: 250_000},
		},
	}
	handler := costTestRouter(NewServer(stub))

	recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/estimate?profession=Повар&type=chat", "rop")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	body := decodeEstimateBody(t, recorder)
	// 250 000 / 100 = 2500 tokens per call * 7 expected chat calls.
	if body.SampleSize != 3 || body.EstimatedDurationSeconds != 60 || body.EstimatedTokens != 17_500 {
		t.Fatalf("global fallback = %+v", body)
	}
	if body.Method != estimateMethodGlobal || body.Confidence != estimateConfidenceMedium {
		t.Fatalf("method/confidence = %q/%q", body.Method, body.Confidence)
	}
	if body.EstimatedCostUSD != nil {
		t.Fatalf("cost = %v, want null", *body.EstimatedCostUSD)
	}
}

func TestRunEstimateCostSources(t *testing.T) {
	t.Run("profession history wins", func(t *testing.T) {
		history := CostHistory{ProfessionRuns: []CostRunSample{
			{RunID: "r1", DurationSeconds: 60, UsageRows: 1, TotalTokens: 1000, CostUSD: costPointer(0.01)},
			{RunID: "r2", DurationSeconds: 60, UsageRows: 1, TotalTokens: 1000, CostUSD: costPointer(0.02)},
		}}
		body := buildRunEstimate("cook", domain.RunTypeBatch, history, 0)
		if body.EstimatedCostUSD == nil || *body.EstimatedCostUSD != 0.015 {
			t.Fatalf("cost = %v, want 0.015", body.EstimatedCostUSD)
		}
	})

	t.Run("price derived from usage", func(t *testing.T) {
		history := CostHistory{RequestStats: LLMRequestStats{
			RequestCount: 2, TotalTokens: 10_000,
			CostRequestCount: 2, CostTokens: 10_000, CostSumUSD: 0.02,
		}}
		// Global average is 5000 tokens per call, batch makes one expected call,
		// so the token estimate is 5000. Price = 0.02/10000 = 0.000002 USD per
		// token, and the derived cost is 0.01 USD.
		body := buildRunEstimate("cook", domain.RunTypeBatch, history, 0)
		if body.EstimatedTokens != 5000 {
			t.Fatalf("tokens = %d", body.EstimatedTokens)
		}
		if body.EstimatedCostUSD == nil || *body.EstimatedCostUSD != 0.01 {
			t.Fatalf("cost = %v, want 0.01", body.EstimatedCostUSD)
		}
	})

	t.Run("configured price", func(t *testing.T) {
		body := buildRunEstimate("cook", domain.RunTypeBatch, CostHistory{}, 2)
		if body.EstimatedCostUSD == nil || *body.EstimatedCostUSD != 0.008 {
			t.Fatalf("cost = %v, want 0.008", body.EstimatedCostUSD)
		}
	})

	t.Run("unknown stays null", func(t *testing.T) {
		body := buildRunEstimate("cook", domain.RunTypeChat, CostHistory{}, 0)
		if body.EstimatedCostUSD != nil {
			t.Fatalf("cost = %v, want null", *body.EstimatedCostUSD)
		}
	})
}

func TestRunEstimateBounds(t *testing.T) {
	history := CostHistory{ProfessionRuns: []CostRunSample{
		{RunID: "bad-duration", DurationSeconds: -10, UsageRows: 1, TotalTokens: -5},
		{RunID: "tiny", DurationSeconds: 0.2, UsageRows: 1, TotalTokens: 0, CostUSD: costPointer(-1)},
		{RunID: "huge", DurationSeconds: 7200, UsageRows: 1, TotalTokens: 1000},
	}}
	body := buildRunEstimate("cook", domain.RunTypeChat, history, 0)
	if body.EstimatedDurationSeconds < 1 {
		t.Fatalf("duration = %d, want >= 1", body.EstimatedDurationSeconds)
	}
	if body.EstimatedTokens < 0 {
		t.Fatalf("tokens = %d, want >= 0", body.EstimatedTokens)
	}
	if body.EstimatedCostUSD != nil {
		t.Fatalf("negative cost must be ignored, got %v", *body.EstimatedCostUSD)
	}
	// The invalid duration is ignored, the two valid ones are 0.2 and 7200.
	if body.SampleSize != 2 || body.EstimatedDurationSeconds != 3600 {
		t.Fatalf("sample/duration = %d/%d, want 2/3600", body.SampleSize, body.EstimatedDurationSeconds)
	}
	// Zero tokens from a usage row is a valid sample; the positive sample makes
	// the mean 500 (tokens are never invented, only averaged).
	if body.EstimatedTokens != 500 {
		t.Fatalf("tokens = %d, want 500", body.EstimatedTokens)
	}
}

func TestRunEstimateProviderError(t *testing.T) {
	runStore := newMemStore()
	stub := &costStubStore{Store: runStore, historyErr: errors.New("database is down")}
	handler := costTestRouter(NewServer(stub))

	recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/estimate?profession=cook&type=chat", "admin")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestRunCostNoData(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(90 * time.Second)
	runStore := newMemStore()
	runStore.seedRun(seededRun("run-empty", "Повар", domain.RunTypeChat, domain.RunDone, started, finished))
	handler := costTestRouter(NewServer(runStore))

	recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/run-empty/cost", "guest")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	body := decodeCostBody(t, recorder)
	if body.RunID != "run-empty" || body.Profession != "Повар" || body.Type != domain.RunTypeChat || body.Status != domain.RunDone {
		t.Fatalf("identity = %+v", body)
	}
	if body.RequestCount != 0 || body.TotalTokens != 0 || body.PromptTokens != 0 || body.CompletionTokens != 0 {
		t.Fatalf("empty facts = %+v", body)
	}
	if body.CostUSD != nil {
		t.Fatalf("cost = %v, want null", *body.CostUSD)
	}
	if body.Attribution != estimateAttributionNone || body.AttributionAmbiguous {
		t.Fatalf("attribution = %q ambiguous=%v", body.Attribution, body.AttributionAmbiguous)
	}
	if body.DurationSeconds == nil || *body.DurationSeconds != 90 {
		t.Fatalf("duration = %v, want 90", body.DurationSeconds)
	}
	if body.ByModel == nil || len(body.ByModel) != 0 || body.ByAgent == nil || len(body.ByAgent) != 0 || body.StatusCounts == nil || len(body.StatusCounts) != 0 {
		t.Fatalf("empty facts must be empty arrays, got %+v", body)
	}
	if body.Estimate.Method != estimateMethodDefaults || body.Estimate.SampleSize != 0 {
		t.Fatalf("estimate = %+v", body.Estimate)
	}
	if !strings.Contains(recorder.Body.String(), `"cost_usd":null`) {
		t.Fatalf("cost must serialize as explicit null: %s", recorder.Body.String())
	}
}

func TestRunCostFactsAggregation(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(120 * time.Second)
	runStore := newMemStore()
	runStore.seedRun(seededRun("run-facts", "Повар", domain.RunTypeBatch, domain.RunDone, started, finished))
	stub := &costStubStore{
		Store: runStore,
		facts: RunLLMFacts{
			Attribution: estimateAttributionRunID,
			Requests: []LLMRequestFact{
				{Agent: "interviewer", Model: "model-a", Status: "ok", PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, LatencyMS: 1000, CostUSD: costPointer(0.001)},
				{Agent: "interviewer", Model: "model-a", Status: "ok", PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, LatencyMS: 1200},
				{Agent: "analyst", Model: "model-b", Status: "error", PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300, LatencyMS: 500, CostUSD: costPointer(0.002)},
			},
		},
		history: CostHistory{ProfessionRuns: []CostRunSample{
			{RunID: "other", DurationSeconds: 60, UsageRows: 1, TotalTokens: 500},
		}},
	}
	handler := costTestRouter(NewServer(stub))

	recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/run-facts/cost", "student")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	body := decodeCostBody(t, recorder)
	if stub.factsRunID != "run-facts" {
		t.Fatalf("facts run id = %q", stub.factsRunID)
	}
	if body.RequestCount != 3 || body.PromptTokens != 400 || body.CompletionTokens != 200 || body.TotalTokens != 600 {
		t.Fatalf("totals = %+v", body)
	}
	if body.CostUSD == nil || *body.CostUSD != 0.003 {
		t.Fatalf("cost = %v, want 0.003", body.CostUSD)
	}
	if body.DurationSeconds == nil || *body.DurationSeconds != 120 {
		t.Fatalf("duration = %v, want 120", body.DurationSeconds)
	}
	if body.Attribution != estimateAttributionRunID || body.AttributionAmbiguous {
		t.Fatalf("attribution = %q ambiguous=%v", body.Attribution, body.AttributionAmbiguous)
	}
	if len(body.ByModel) != 2 {
		t.Fatalf("by_model = %+v", body.ByModel)
	}
	// model-a has 300 total tokens, model-b 300: tie is broken by name, then a
	// stable order is kept by the sort (totals equal, "model-a" < "model-b").
	if body.ByModel[0].Model != "model-a" || body.ByModel[0].RequestCount != 2 || body.ByModel[0].TotalTokens != 300 {
		t.Fatalf("by_model[0] = %+v", body.ByModel[0])
	}
	if body.ByModel[1].Model != "model-b" || body.ByModel[1].RequestCount != 1 || body.ByModel[1].CostUSD == nil || *body.ByModel[1].CostUSD != 0.002 {
		t.Fatalf("by_model[1] = %+v", body.ByModel[1])
	}
	// Both groups have 300 tokens, so the tie is broken by name: "analyst"
	// sorts before "interviewer".
	if len(body.ByAgent) != 2 || body.ByAgent[0].Agent != "analyst" || body.ByAgent[0].RequestCount != 1 || body.ByAgent[1].Agent != "interviewer" || body.ByAgent[1].RequestCount != 2 {
		t.Fatalf("by_agent = %+v", body.ByAgent)
	}
	if len(body.StatusCounts) != 2 || body.StatusCounts[0] != (StatusCount{Status: "error", Count: 1}) || body.StatusCounts[1] != (StatusCount{Status: "ok", Count: 2}) {
		t.Fatalf("status_counts = %+v", body.StatusCounts)
	}
	// Sample of the estimate equals the fake history (the current run is not in it).
	if body.Estimate.SampleSize != 1 || body.Estimate.Method != estimateMethodProfession {
		t.Fatalf("estimate = %+v", body.Estimate)
	}
}

func TestRunCostTimeWindowAttribution(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(60 * time.Second)
	runStore := newMemStore()
	runStore.seedRun(seededRun("run-window", "Повар", domain.RunTypeChat, domain.RunDone, started, finished))
	stub := &costStubStore{
		Store: runStore,
		facts: RunLLMFacts{
			Attribution: estimateAttributionTimeWindow,
			Ambiguous:   true,
			Requests: []LLMRequestFact{
				{Agent: "interviewer", Model: "model-a", Status: "ok", TotalTokens: 100},
			},
		},
	}
	handler := costTestRouter(NewServer(stub))

	body := decodeCostBody(t, costDo(t, handler, http.MethodGet, "/api/v1/runs/run-window/cost", "teacher"))
	if body.Attribution != estimateAttributionTimeWindow || !body.AttributionAmbiguous {
		t.Fatalf("attribution = %q ambiguous=%v", body.Attribution, body.AttributionAmbiguous)
	}
	if body.RequestCount != 1 || body.TotalTokens != 100 {
		t.Fatalf("facts = %+v", body)
	}
}

func TestRunCostUnknownRun(t *testing.T) {
	handler := costTestRouter(NewServer(NewMemStoreForTest()))
	recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/missing/cost", "admin")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestRunCostAuth(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(10 * time.Second)
	runStore := newMemStore()
	runStore.seedRun(seededRun("run-auth", "Повар", domain.RunTypeBatch, domain.RunDone, started, finished))
	handler := costTestRouter(NewServer(runStore))
	const target = "/api/v1/runs/run-auth/cost"

	if recorder := costDo(t, handler, http.MethodGet, target, ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", recorder.Code)
	}
	// The cost endpoint matches the read roles of GET /runs/{id}.
	for _, role := range []string{"admin", "teacher", "rop", "student", "guest"} {
		if recorder := costDo(t, handler, http.MethodGet, target, role); recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (%s)", role, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRunCostStorageErrors(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(10 * time.Second)

	t.Run("run lookup error", func(t *testing.T) {
		runStore := newMemStore()
		stub := &costStubStore{Store: runStore, getRunErr: errors.New("connection reset")}
		handler := costTestRouter(NewServer(stub))
		recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/run-x/cost", "admin")
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (%s)", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("facts error", func(t *testing.T) {
		runStore := newMemStore()
		runStore.seedRun(seededRun("run-x", "Повар", domain.RunTypeChat, domain.RunDone, started, finished))
		stub := &costStubStore{Store: runStore, factsErr: errors.New("query failed")}
		handler := costTestRouter(NewServer(stub))
		recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/run-x/cost", "admin")
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (%s)", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("estimate history error degrades", func(t *testing.T) {
		runStore := newMemStore()
		runStore.seedRun(seededRun("run-x", "Повар", domain.RunTypeChat, domain.RunDone, started, finished))
		stub := &costStubStore{
			Store:      runStore,
			historyErr: errors.New("history query failed"),
			facts: RunLLMFacts{
				Attribution: estimateAttributionRunID,
				Requests:    []LLMRequestFact{{Agent: "interviewer", Model: "m", Status: "ok", TotalTokens: 100}},
			},
		}
		handler := costTestRouter(NewServer(stub))
		recorder := costDo(t, handler, http.MethodGet, "/api/v1/runs/run-x/cost", "admin")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
		}
		body := decodeCostBody(t, recorder)
		if body.RequestCount != 1 || body.Estimate.Method != estimateMethodDefaults {
			t.Fatalf("body = %+v", body)
		}
	})
}

func TestRunCostExcludesCurrentRunFromEstimate(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(60 * time.Second)
	runStore := newMemStore()
	runStore.seedRun(seededRun("run-self", "Повар", domain.RunTypeChat, domain.RunDone, started, finished))
	stub := &costStubStore{
		Store: runStore,
		history: CostHistory{ProfessionRuns: []CostRunSample{
			{RunID: "run-self", DurationSeconds: 1000, UsageRows: 1, TotalTokens: 9999},
			{RunID: "run-previous", DurationSeconds: 60, UsageRows: 1, TotalTokens: 1000},
		}},
	}
	handler := costTestRouter(NewServer(stub))
	body := decodeCostBody(t, costDo(t, handler, http.MethodGet, "/api/v1/runs/run-self/cost", "admin"))
	if body.Estimate.SampleSize != 1 || body.Estimate.EstimatedDurationSeconds != 60 || body.Estimate.EstimatedTokens != 1000 {
		t.Fatalf("estimate = %+v, want sample 1 dur 60 tokens 1000", body.Estimate)
	}
}

func TestRunCostRunningDuration(t *testing.T) {
	started := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	now := started.Add(150 * time.Second)
	run := &domain.Run{ID: "run-live", Profession: "Повар", Type: domain.RunTypeChat, Status: domain.RunRunning, StartedAt: &started}
	body := buildRunCostResponse(run, RunLLMFacts{Attribution: estimateAttributionNone}, CostHistory{}, 0, now)
	if body.DurationSeconds == nil || *body.DurationSeconds != 150 {
		t.Fatalf("running duration = %v, want 150", body.DurationSeconds)
	}

	notStarted := &domain.Run{ID: "run-pending", Profession: "Повар", Type: domain.RunTypeBatch, Status: domain.RunPending}
	body = buildRunCostResponse(notStarted, RunLLMFacts{}, CostHistory{}, 0, now)
	if body.DurationSeconds != nil {
		t.Fatalf("pending duration = %v, want null", *body.DurationSeconds)
	}
	if body.Attribution != estimateAttributionNone {
		t.Fatalf("empty attribution = %q, want %q", body.Attribution, estimateAttributionNone)
	}
	// A run without an explicit type is the historical batch default.
	if body.Type != domain.RunTypeBatch {
		t.Fatalf("type = %q, want batch", body.Type)
	}
}

func TestBuildRunEstimateDeterministic(t *testing.T) {
	history := CostHistory{
		ProfessionRuns: []CostRunSample{
			{RunID: "r1", DurationSeconds: 60, UsageRows: 3, TotalTokens: 1500, CostUSD: costPointer(0.01)},
			{RunID: "r2", DurationSeconds: 90, UsageRows: 3, TotalTokens: 2500, CostUSD: costPointer(0.03)},
			{RunID: "r3", DurationSeconds: 120, UsageRows: 3, TotalTokens: 3500},
		},
		RequestStats: LLMRequestStats{RequestCount: 9, TotalTokens: 7500},
	}
	first := buildRunEstimate("cook", domain.RunTypeChat, history, 0)
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal estimate: %v", err)
	}
	for i := 0; i < 10; i++ {
		nextJSON, marshalErr := json.Marshal(buildRunEstimate("cook", domain.RunTypeChat, history, 0))
		if marshalErr != nil {
			t.Fatalf("marshal estimate: %v", marshalErr)
		}
		if string(nextJSON) != string(firstJSON) {
			t.Fatalf("estimate is not deterministic: %s vs %s", nextJSON, firstJSON)
		}
	}
	// Median duration 90, mean tokens 2500, mean recorded cost 0.02, sample 3
	// gives medium confidence (>=3 samples, <10).
	if first.EstimatedDurationSeconds != 90 || first.EstimatedTokens != 2500 {
		t.Fatalf("estimate = %+v", first)
	}
	if first.EstimatedCostUSD == nil || *first.EstimatedCostUSD != 0.02 {
		t.Fatalf("cost = %v, want 0.02", first.EstimatedCostUSD)
	}
	if first.Confidence != estimateConfidenceMedium || first.Method != estimateMethodProfession {
		t.Fatalf("confidence/method = %q/%q", first.Confidence, first.Method)
	}
}

func TestBuildRunEstimateHighConfidence(t *testing.T) {
	var samples []CostRunSample
	for i := 0; i < 12; i++ {
		samples = append(samples, CostRunSample{RunID: "r", DurationSeconds: float64(60 + i), UsageRows: 1, TotalTokens: 1000})
	}
	body := buildRunEstimate("cook", domain.RunTypeBatch, CostHistory{ProfessionRuns: samples}, 0)
	if body.Confidence != estimateConfidenceHigh || body.SampleSize != 12 {
		t.Fatalf("confidence/sample = %q/%d, want high/12", body.Confidence, body.SampleSize)
	}
}

func costPointer(value float64) *float64 { return &value }
