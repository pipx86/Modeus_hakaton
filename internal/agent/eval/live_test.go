package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/agents"
)

// liveStubClient is the simplest controllable llm.ChatClient for live-harness
// unit tests. It never touches the network.
type liveStubClient struct {
	answer string
	err    error
	calls  int
}

func (c *liveStubClient) Chat(_ context.Context, _ []llm.Message) (string, error) {
	c.calls++
	return c.answer, c.err
}

// blockingLiveClient waits for the case context and returns its error. It
// exercises the live harness timeout path without sleeping longer than needed.
type blockingLiveClient struct{}

func (*blockingLiveClient) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// liveScriptFor builds a schema-valid scripted analyst answer quoting the
// scripted respondent answers of tc. It lets the offline script client serve
// the live pipeline path in tests without a provider.
func liveScriptFor(tc LiveCase) Script {
	return Script{Analyst: agent.AnalystOutput{Entities: []agent.ExtractedEntity{
		{Node: domain.Node{
			ID:    "role-analyst",
			Type:  domain.NodeRole,
			Title: "Аналитик данных",
			Evidence: []domain.Evidence{{
				ID: "ev-role-live", SourceID: "src-interview", RefID: "answer-1", Quote: tc.Answers.Role,
			}},
		}, Confidence: 0.95},
		{Node: domain.Node{
			ID:    "fn-reporting",
			Type:  domain.NodeFunction,
			Title: "Отчётность и визуализация",
			Evidence: []domain.Evidence{{
				ID: "ev-fn-live", SourceID: "src-interview", RefID: "answer-2", Quote: tc.Answers.Function,
			}},
		}, Confidence: 0.9},
		{Node: domain.Node{
			ID:    "task-sql",
			Type:  domain.NodeTask,
			Title: "Писать SQL-запросы",
			Evidence: []domain.Evidence{{
				ID: "ev-task-live", SourceID: "src-interview", RefID: "answer-3", Quote: tc.Answers.Task,
			}},
		}, Confidence: 0.9},
		{Node: domain.Node{
			ID:    "act-optimize",
			Type:  domain.NodeAction,
			Title: "Оптимизировать SQL-запросы",
			Evidence: []domain.Evidence{{
				ID: "ev-act-live", SourceID: "src-interview", RefID: "answer-4", Quote: tc.Answers.Action,
			}},
		}, Confidence: 0.85},
	}}}
}

// fakeLiveClients returns clients backed by the offline script client, wrapped
// in a recorder so the live metrics are exercised exactly like a real run.
func fakeLiveClients(t *testing.T, tc LiveCase) (LiveClients, *CallRecorder) {
	t.Helper()
	fake := newScriptClient(liveScriptFor(tc))
	recorder := NewCallRecorder()
	return LiveClients{
		Interviewer: recorder.Wrap(string(agents.Interviewer), "fake-interviewer", fake),
		Analyst:     recorder.Wrap(string(agents.Analyst), "fake-analyst", fake),
		Recorder:    recorder,
		Models: map[string]string{
			string(agents.Interviewer): "fake-interviewer",
			string(agents.Analyst):     "fake-analyst",
		},
	}, recorder
}

func requireLiveCase(t *testing.T, name string) LiveCase {
	t.Helper()
	tc, ok := LiveCaseByName(name)
	if !ok {
		t.Fatalf("built-in live case %q not found", name)
	}
	return tc
}

func TestDefaultLiveCasesAreValid(t *testing.T) {
	cases := DefaultLiveCases()
	if len(cases) != 2 {
		t.Fatalf("want 2 built-in live cases, got %d", len(cases))
	}
	if err := ValidateLiveCases(cases); err != nil {
		t.Fatalf("ValidateLiveCases: %v", err)
	}
	baseline := requireLiveCase(t, LiveCaseBaseline)
	if len(baseline.Standard) != 0 || len(baseline.Vacancy) != 0 {
		t.Errorf("baseline must stay interview-only, got %d standard and %d vacancy nodes", len(baseline.Standard), len(baseline.Vacancy))
	}
	conflict := requireLiveCase(t, LiveCaseConflict)
	if conflict.Expect.MinConflicts < 1 {
		t.Errorf("conflict case must assert at least one conflict, got %d", conflict.Expect.MinConflicts)
	}
	if len(conflict.Standard) == 0 || len(conflict.Vacancy) == 0 {
		t.Fatalf("conflict case needs standard and vacancy nodes")
	}
	sourceByID := make(map[string]domain.Source, len(conflict.Sources))
	for _, source := range conflict.Sources {
		sourceByID[source.ID] = source
	}
	for _, id := range []string{"src-standard", "src-vacancy"} {
		if strings.TrimSpace(sourceByID[id].Content) == "" {
			t.Errorf("source %q must carry content so the analyst can run the focused source extraction", id)
		}
	}
}

func TestValidateLiveCaseRejectsBadInput(t *testing.T) {
	valid := requireLiveCase(t, LiveCaseConflict)
	tests := []struct {
		name   string
		mutate func(*LiveCase)
		want   string
	}{
		{"empty name", func(c *LiveCase) { c.Name = " " }, "name is required"},
		{"missing answer", func(c *LiveCase) { c.Answers.Action = "" }, "answers.action is required"},
		{"no sources", func(c *LiveCase) { c.Sources = nil }, "at least one source is required"},
		{"duplicate source", func(c *LiveCase) {
			c.Sources = append(c.Sources, domain.Source{ID: "src-interview", Type: domain.SourceInterview, Title: "dup"})
		}, "duplicate source id"},
		{"invalid source type", func(c *LiveCase) { c.Sources[0].Type = "mystery" }, "invalid type"},
		{"undeclared node source", func(c *LiveCase) {
			c.Standard[0].Evidence[0].SourceID = "src-missing"
		}, "references source"},
		{"wrong node source type", func(c *LiveCase) {
			c.Standard[0].Evidence[0].SourceID = "src-vacancy"
		}, "want a declared \"standard\" source"},
		{"invalid node type", func(c *LiveCase) { c.Vacancy[0].Type = "mystery" }, "invalid type"},
		{"negative conflicts", func(c *LiveCase) { c.Expect.MinConflicts = -1 }, "min_conflicts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Standard = append([]domain.Node(nil), valid.Standard...)
			candidate.Vacancy = append([]domain.Node(nil), valid.Vacancy...)
			candidate.Sources = append([]domain.Source(nil), valid.Sources...)
			candidate.Standard[0].Evidence = append([]domain.Evidence(nil), valid.Standard[0].Evidence...)
			test.mutate(&candidate)
			err := candidate.Validate()
			if err == nil {
				t.Fatalf("Validate succeeded, want an error containing %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), test.want)
			}
		})
	}
}

func TestValidateLiveCasesRejectsDuplicates(t *testing.T) {
	cases := DefaultLiveCases()
	cases = append(cases, cases[0])
	err := ValidateLiveCases(cases)
	if err == nil || !strings.Contains(err.Error(), "duplicate case name") {
		t.Fatalf("error = %v, want a duplicate case name error", err)
	}
}

func TestRunLiveCaseWithFakeClients(t *testing.T) {
	tc := requireLiveCase(t, LiveCaseBaseline)
	clients, _ := fakeLiveClients(t, tc)
	runner := LiveRunner{
		Clients:     clients,
		CaseTimeout: 10 * time.Second,
		Provider:    LiveProviderInfo{Models: clients.Models},
	}
	report := runner.RunAll(context.Background(), []LiveCase{tc})
	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("report = passed %d, failed %d; want 1/0", report.Passed, report.Failed)
	}
	caseReport := report.Cases[0]
	if !caseReport.Passed {
		t.Fatalf("case failed: %v", caseReport.Failures)
	}
	metrics := caseReport.Metrics
	if metrics.Status != "done" {
		t.Errorf("status = %q, want done", metrics.Status)
	}
	if !metrics.ContractValid {
		t.Error("contract_valid = false, want true")
	}
	if metrics.Questions != 4 || metrics.Clarifications != 0 {
		t.Errorf("questions/clarifications = %d/%d, want 4/0", metrics.Questions, metrics.Clarifications)
	}
	for _, nodeType := range []string{"role", "function", "task", "action"} {
		if metrics.NodeTypes[nodeType] == 0 {
			t.Errorf("tree has no %q nodes: %v", nodeType, metrics.NodeTypes)
		}
	}
	if metrics.TreeNodes < 4 {
		t.Errorf("tree_nodes = %d, want at least 4", metrics.TreeNodes)
	}
	if metrics.Evidence < 4 {
		t.Errorf("evidence = %d, want at least 4", metrics.Evidence)
	}
	if metrics.ModelCalls != 5 {
		t.Errorf("model_calls = %d, want 5 (four questions + one analyst call)", metrics.ModelCalls)
	}
	if metrics.ModelErrors != 0 || metrics.FallbackCalls != 0 {
		t.Errorf("model_errors/fallback_calls = %d/%d, want 0/0", metrics.ModelErrors, metrics.FallbackCalls)
	}
	if got, want := strings.Join(metrics.Models, ","), "fake-analyst,fake-interviewer"; got != want {
		t.Errorf("models = %q, want %q", got, want)
	}
	if metrics.CallsByAgent["interviewer"] != 4 || metrics.CallsByAgent["analyst"] != 1 {
		t.Errorf("calls_by_agent = %v, want interviewer 4 and analyst 1", metrics.CallsByAgent)
	}
}

func TestRunLiveCaseConflictUsesProvidedNodes(t *testing.T) {
	tc := requireLiveCase(t, LiveCaseConflict)
	clients, _ := fakeLiveClients(t, tc)
	runner := LiveRunner{Clients: clients, CaseTimeout: 10 * time.Second}
	caseReport := runner.RunCase(context.Background(), tc)
	if !caseReport.Passed {
		t.Fatalf("conflict case failed: %v", caseReport.Failures)
	}
	if caseReport.Metrics.Conflicts < 1 {
		t.Fatalf("conflicts = %d, want at least 1 from the provided standard/vacancy nodes", caseReport.Metrics.Conflicts)
	}
	if caseReport.Metrics.FallbackQuestions != 0 {
		t.Errorf("fallback_questions = %d, want 0", caseReport.Metrics.FallbackQuestions)
	}
}

func TestRunLiveCaseFallbackIsMeasured(t *testing.T) {
	tc := requireLiveCase(t, LiveCaseBaseline)
	fake := newScriptClient(liveScriptFor(tc))
	recorder := NewCallRecorder()
	primary := &liveStubClient{err: llm.ErrEmptyResponse}
	buildChain := func(agentName string) llm.ChatClient {
		t.Helper()
		chain, err := llm.NewFallbackClient([]llm.FallbackAttempt{
			{Model: "fake-primary", Client: recorder.Wrap(agentName, "fake-primary", primary)},
			{Model: "fake-fallback", Client: recorder.Wrap(agentName, "fake-fallback", fake)},
		})
		if err != nil {
			t.Fatalf("NewFallbackClient: %v", err)
		}
		return chain
	}
	clients := LiveClients{
		Interviewer: buildChain(string(agents.Interviewer)),
		Analyst:     buildChain(string(agents.Analyst)),
		Recorder:    recorder,
		Models: map[string]string{
			string(agents.Interviewer): "fake-primary",
			string(agents.Analyst):     "fake-primary",
		},
	}
	runner := LiveRunner{Clients: clients, CaseTimeout: 10 * time.Second}
	caseReport := runner.RunCase(context.Background(), tc)
	if !caseReport.Passed {
		t.Fatalf("case failed: %v", caseReport.Failures)
	}
	metrics := caseReport.Metrics
	if metrics.Status != "done" {
		t.Errorf("status = %q, want done (the fallback answered every call)", metrics.Status)
	}
	if metrics.ModelCalls != 10 {
		t.Errorf("model_calls = %d, want 10 (5 failed primary + 5 fallback)", metrics.ModelCalls)
	}
	if metrics.ModelErrors != 5 || metrics.FallbackCalls != 5 || metrics.FallbackServed != 5 {
		t.Errorf("errors/fallback/fallback-served = %d/%d/%d, want 5/5/5",
			metrics.ModelErrors, metrics.FallbackCalls, metrics.FallbackServed)
	}
	if got, want := strings.Join(metrics.Models, ","), "fake-fallback,fake-primary"; got != want {
		t.Errorf("models = %q, want %q", got, want)
	}
	if !strings.Contains(metrics.FirstError, "empty response") {
		t.Errorf("first_error = %q, want the empty-response provider error", metrics.FirstError)
	}
}

func TestRunLiveCaseTimeout(t *testing.T) {
	tc := requireLiveCase(t, LiveCaseBaseline)
	recorder := NewCallRecorder()
	blocking := &blockingLiveClient{}
	clients := LiveClients{
		Interviewer: recorder.Wrap("interviewer", "fake-model", blocking),
		Analyst:     recorder.Wrap("analyst", "fake-model", blocking),
		Recorder:    recorder,
		Models:      map[string]string{"interviewer": "fake-model", "analyst": "fake-model"},
	}
	runner := LiveRunner{Clients: clients, CaseTimeout: 100 * time.Millisecond}
	start := time.Now()
	caseReport := runner.RunCase(context.Background(), tc)
	elapsed := time.Since(start)
	if caseReport.Passed {
		t.Fatal("timeout case reported PASS")
	}
	if !strings.Contains(caseReport.Error, "case timeout after") {
		t.Fatalf("error = %q, want a case timeout", caseReport.Error)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("elapsed = %s, want at least the 100ms case timeout", elapsed)
	}
	// The pipeline returns no partial Result on failure, so only the recorded
	// model attempts are available; the session counters are not.
	if caseReport.Metrics.ModelErrors == 0 {
		t.Error("model_errors = 0, want the timed-out attempts recorded")
	}
	if caseReport.Metrics.ModelCalls == 0 {
		t.Error("model_calls = 0, want the timed-out attempts recorded")
	}
}

func TestRunLiveCaseRequiresClients(t *testing.T) {
	tc := requireLiveCase(t, LiveCaseBaseline)
	caseReport := LiveRunner{}.RunCase(context.Background(), tc)
	if caseReport.Passed || caseReport.Error == "" {
		t.Fatalf("report = %+v, want a client error and no pass", caseReport)
	}
}

func TestRunLiveCaseRedactsSecrets(t *testing.T) {
	const secret = "sk-live-eval-unit-test-secret"
	tc := requireLiveCase(t, LiveCaseBaseline)
	recorder := NewCallRecorder()
	failing := &liveStubClient{err: fmt.Errorf("provider rejected key %s", secret)}
	clients := LiveClients{
		Interviewer: recorder.Wrap("interviewer", "fake-model", failing),
		Analyst:     recorder.Wrap("analyst", "fake-model", failing),
		Recorder:    recorder,
		Models:      map[string]string{"interviewer": "fake-model", "analyst": "fake-model"},
	}
	runner := LiveRunner{Clients: clients, CaseTimeout: 5 * time.Second, RedactSecrets: []string{secret}}
	caseReport := runner.RunCase(context.Background(), tc)
	if caseReport.Passed {
		t.Fatal("failing provider case reported PASS")
	}
	for name, value := range map[string]string{
		"error":         caseReport.Error,
		"failure":       strings.Join(caseReport.Failures, " "),
		"first_error":   caseReport.Metrics.FirstError,
		"call_trace[0]": caseReport.Metrics.CallTrace[0].Error,
	} {
		if strings.Contains(value, secret) {
			t.Errorf("%s leaks the API key: %q", name, value)
		}
		if !strings.Contains(value, "[REDACTED]") && name != "failure" {
			t.Errorf("%s = %q, want it to contain [REDACTED]", name, value)
		}
	}
}

func TestCallRecorder(t *testing.T) {
	recorder := NewCallRecorder()
	stub := &liveStubClient{answer: "ok"}
	wrapped := recorder.Wrap("interviewer", "model-a", stub)
	if _, err := wrapped.Chat(context.Background(), nil); err != nil {
		t.Fatalf("wrapped Chat: %v", err)
	}
	recorder.Observe("analyst", "model-b", 5*time.Millisecond, errors.New("boom"))
	if got := recorder.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	since := recorder.CallsSince(1)
	if len(since) != 1 || since[0].Agent != "analyst" || since[0].Error != "boom" {
		t.Fatalf("CallsSince(1) = %+v", since)
	}
	all := recorder.Calls()
	all[0].Agent = "mutated"
	if recorder.Calls()[0].Agent != "interviewer" {
		t.Fatal("Calls returned a slice sharing the recorder storage")
	}

	var nilRecorder *CallRecorder
	if nilRecorder.Wrap("interviewer", "model", stub) != stub {
		t.Fatal("nil recorder must return the client unchanged")
	}
	if nilRecorder.Len() != 0 || nilRecorder.Calls() != nil {
		t.Fatal("nil recorder must be a no-op")
	}

	decorator := RecordingDecorator(recorder)
	decorated := decorator(agents.Interviewer, "model-c", stub)
	if _, err := decorated.Chat(context.Background(), nil); err != nil {
		t.Fatalf("decorated Chat: %v", err)
	}
	if got := recorder.Len(); got != 3 {
		t.Fatalf("Len after decorator call = %d, want 3", got)
	}
	if last := recorder.Calls()[2]; last.Agent != string(agents.Interviewer) || last.Model != "model-c" {
		t.Fatalf("decorator call = %+v", last)
	}
}

func TestWriteLivePlanAndTable(t *testing.T) {
	provider := LiveProviderInfo{
		BaseURL: "https://example.invalid/v1",
		Models: map[string]string{
			"interviewer": "model-interviewer",
			"analyst":     "model-analyst",
		},
		FallbackModels: map[string]string{"analyst": "model-analyst-fallback"},
		Synthesizer:    "deterministic (no model call)",
	}
	var plan bytes.Buffer
	if err := WriteLivePlan(&plan, provider, DefaultLiveCases(), time.Minute); err != nil {
		t.Fatalf("WriteLivePlan: %v", err)
	}
	for _, fragment := range []string{
		"LIVE AGENT PIPELINE EVAL", "https://example.invalid/v1",
		"interviewer: model=model-interviewer", "fallback=model-analyst-fallback",
		"dry run:", LiveCaseBaseline, LiveCaseConflict, "case timeout: 1m0s",
	} {
		if !strings.Contains(plan.String(), fragment) {
			t.Errorf("plan does not contain %q:\n%s", fragment, plan.String())
		}
	}

	tc := requireLiveCase(t, LiveCaseBaseline)
	clients, _ := fakeLiveClients(t, tc)
	report := LiveRunner{Clients: clients, Provider: provider, CaseTimeout: time.Minute}.
		RunAll(context.Background(), []LiveCase{tc})

	var table bytes.Buffer
	if err := WriteLiveTable(&table, report); err != nil {
		t.Fatalf("WriteLiveTable: %v", err)
	}
	for _, fragment := range []string{
		"LIVE AGENT PIPELINE EVAL", LiveCaseBaseline, "PASS",
		"MODEL CALLS", "fake-interviewer", "fake-analyst",
		"SUMMARY: 1/1 cases passed", "contract valid 1/1",
	} {
		if !strings.Contains(table.String(), fragment) {
			t.Errorf("table does not contain %q:\n%s", fragment, table.String())
		}
	}

	var payload bytes.Buffer
	if err := WriteLiveJSON(&payload, report); err != nil {
		t.Fatalf("WriteLiveJSON: %v", err)
	}
	var decoded LiveReport
	if err := json.Unmarshal(payload.Bytes(), &decoded); err != nil {
		t.Fatalf("live report is not valid JSON: %v", err)
	}
	if decoded.SchemaVersion != LiveSchemaVersion || decoded.Mode != "live" {
		t.Errorf("decoded report = version %d mode %q", decoded.SchemaVersion, decoded.Mode)
	}
	if decoded.Passed != 1 || len(decoded.Cases) != 1 || decoded.Cases[0].Name != LiveCaseBaseline {
		t.Errorf("decoded report = %+v", decoded)
	}
}

func TestWriteLiveTableReportsFailures(t *testing.T) {
	report := LiveReport{
		SchemaVersion: LiveSchemaVersion,
		Mode:          "live",
		Failed:        1,
		Cases: []LiveCaseReport{{
			Name:     "broken-case",
			Error:    "pipeline: provider timeout",
			Failures: []string{"pipeline: provider timeout"},
			Metrics:  LiveMetrics{},
		}},
	}
	var table bytes.Buffer
	if err := WriteLiveTable(&table, report); err != nil {
		t.Fatalf("WriteLiveTable: %v", err)
	}
	for _, fragment := range []string{"FAIL", "broken-case", "FAILURES", "provider timeout", "SUMMARY: 0/1 cases passed"} {
		if !strings.Contains(table.String(), fragment) {
			t.Errorf("table does not contain %q:\n%s", fragment, table.String())
		}
	}
}
