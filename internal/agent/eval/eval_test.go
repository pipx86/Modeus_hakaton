package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/pipeline"
)

func defaultCases(t *testing.T) []Case {
	t.Helper()
	cases, err := DefaultCases()
	if err != nil {
		t.Fatalf("DefaultCases: %v", err)
	}
	return cases
}

func caseByName(t *testing.T, name string) Case {
	t.Helper()
	for _, tc := range defaultCases(t) {
		if tc.Name == name {
			return tc
		}
	}
	t.Fatalf("case %q not found in the embedded golden set", name)
	return Case{}
}

func runOne(t *testing.T, name string) pipeline.Result {
	t.Helper()
	result, client, err := execute(context.Background(), caseByName(t, name))
	if err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	if client == nil || client.totalCalls() == 0 {
		t.Fatalf("execute %s: scripted client was not called", name)
	}
	return result
}

func intPtr(value int) *int       { return &value }
func strPtr(value string) *string { return &value }
func boolPtr(value bool) *bool    { return &value }

func failuresContain(failures []string, fragment string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, fragment) {
			return true
		}
	}
	return false
}

func TestDefaultCasesLoadAndValidate(t *testing.T) {
	cases := defaultCases(t)
	if len(cases) < 6 {
		t.Fatalf("want at least 6 golden cases, got %d", len(cases))
	}
	professions := make(map[string]bool)
	names := make(map[string]bool)
	for _, tc := range cases {
		if names[tc.Name] {
			t.Fatalf("duplicate case name %q", tc.Name)
		}
		names[tc.Name] = true
		if err := tc.Validate(); err != nil {
			t.Fatalf("case %q: %v", tc.Name, err)
		}
		if tc.Expect.Status == nil {
			t.Errorf("case %q does not assert the run status", tc.Name)
		}
		if tc.Expect.ModelCalls == nil {
			t.Errorf("case %q does not assert the model call budget", tc.Name)
		}
		professions[tc.Profession] = true
	}
	for _, wanted := range []string{"Аналитик данных", "DevOps-инженер", "Backend-разработчик"} {
		if !professions[wanted] {
			t.Errorf("golden set has no case for profession %q", wanted)
		}
	}
}

func TestAnswersFor(t *testing.T) {
	answers := Answers{Role: "role", Function: "function", Task: "task", Action: "action", Default: "default"}
	tests := []struct {
		level domain.QuestionLevel
		want  string
	}{
		{domain.QuestionRole, "role"},
		{domain.QuestionFunction, "function"},
		{domain.QuestionTask, "task"},
		{domain.QuestionAction, "action"},
		{domain.QuestionLevel("other"), "default"},
	}
	for _, test := range tests {
		if got := answers.For(domain.Question{Level: test.level}); got != test.want {
			t.Errorf("For(%q) = %q, want %q", test.level, got, test.want)
		}
	}
	empty := Answers{Role: "role", Default: "fallback"}
	if got := empty.For(domain.Question{Level: domain.QuestionTask}); got != "fallback" {
		t.Errorf("empty level fell back to %q, want fallback", got)
	}
}

func TestExpectValidate(t *testing.T) {
	valid := Expect{Status: strPtr("done"), Questions: intPtr(6), Conflicts: intPtr(1),
		ConflictWinners: []string{"interview"}, ConflictValues: []int{2}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid expectations rejected: %v", err)
	}
	invalid := map[string]Expect{
		"status":            {Status: strPtr("unknown")},
		"negative question": {Questions: intPtr(-1)},
		"node type":         {NodeTypes: []string{"department"}},
		"winner type":       {ConflictWinners: []string{"hr"}},
		"conflict values":   {ConflictValues: []int{1}},
		"winner count":      {Conflicts: intPtr(2), ConflictWinners: []string{"interview"}},
		"mismatched lists":  {ConflictWinners: []string{"interview"}, ConflictValues: []int{2, 3}},
	}
	for name, expect := range invalid {
		if err := expect.Validate(); err == nil {
			t.Errorf("%s: want validation error, got nil", name)
		}
	}
}

func TestCaseValidateRejectsBadScript(t *testing.T) {
	base := caseByName(t, "data-analyst-baseline")
	badScripts := map[string]func(*Script){
		"unknown question level": func(script *Script) {
			script.Interviewer.Questions = map[string]string{"department": "Чем вы занимаетесь?"}
		},
		"empty question text": func(script *Script) {
			script.Interviewer.Questions = map[string]string{"role": "   "}
		},
		"zero-based error call": func(script *Script) {
			script.Interviewer.ErrorOnCalls = []int{0}
		},
	}
	for name, mutate := range badScripts {
		t.Run(name, func(t *testing.T) {
			mutated := base
			mutate(&mutated.Script)
			if err := mutated.Validate(); err == nil {
				t.Fatalf("want validation error for %s", name)
			}
		})
	}
}

func TestScriptClientRoutesBySchemaContract(t *testing.T) {
	tc := caseByName(t, "data-analyst-baseline")
	client := newScriptClient(tc.Script)
	analystAnswer, err := client.Chat(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "shared security prompt"},
		{Role: llm.RoleUser, Content: "Верни JSON по схеме analyst.output.schema.json."},
	})
	if err != nil {
		t.Fatalf("analyst chat: %v", err)
	}
	var analystOut agent.AnalystOutput
	if err := json.Unmarshal([]byte(analystAnswer), &analystOut); err != nil {
		t.Fatalf("analyst answer is not JSON: %v", err)
	}
	if len(analystOut.Entities) != len(tc.Script.Analyst.Entities) {
		t.Fatalf("analyst entities = %d, want %d", len(analystOut.Entities), len(tc.Script.Analyst.Entities))
	}
	if client.analystCalls != 1 || client.interviewerCalls != 0 {
		t.Fatalf("calls after analyst = interviewer %d / analyst %d, want 0/1",
			client.interviewerCalls, client.analystCalls)
	}

	interviewerAnswer, err := client.Chat(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "shared security prompt"},
		{Role: llm.RoleUser, Content: `{"next_level": "task", "answers": []}` + "\n\nВерни следующий вопрос по схеме interviewer.output.schema.json."},
	})
	if err != nil {
		t.Fatalf("interviewer chat: %v", err)
	}
	var question agent.InterviewerOutput
	if err := json.Unmarshal([]byte(interviewerAnswer), &question); err != nil {
		t.Fatalf("interviewer answer is not JSON: %v", err)
	}
	if question.Question.Level != domain.QuestionTask {
		t.Fatalf("question level = %q, want task", question.Question.Level)
	}
	if strings.TrimSpace(question.Question.Text) == "" {
		t.Fatal("question text is empty")
	}
	if question.IsClarification {
		t.Fatalf("unexpected clarification flag: %v", question.IsClarification)
	}
}

func TestScriptClientInterviewerErrors(t *testing.T) {
	script := Script{Interviewer: InterviewerScript{ErrorOnCalls: []int{1, 2}}}
	client := newScriptClient(script)
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: `"next_level": "role"` + " interviewer.output.schema.json"},
	}
	if _, err := client.Chat(context.Background(), messages); err == nil {
		t.Fatal("call 1: want scripted error")
	}
	if _, err := client.Chat(context.Background(), messages); err == nil {
		t.Fatal("call 2: want scripted error")
	}
	if _, err := client.Chat(context.Background(), messages); err != nil {
		t.Fatalf("call 3: want success, got %v", err)
	}
	if client.interviewerCalls != 3 {
		t.Fatalf("interviewer calls = %d, want 3", client.interviewerCalls)
	}
}

func TestScriptClientRejectsUnknownPrompt(t *testing.T) {
	client := newScriptClient(Script{})
	if _, err := client.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}); err == nil {
		t.Fatal("want error for an unrecognized prompt")
	}
	if _, err := client.Chat(context.Background(), nil); err == nil {
		t.Fatal("want error for empty messages")
	}
}

func TestRunAllGoldenSet(t *testing.T) {
	cases := defaultCases(t)
	report := RunAll(context.Background(), cases)
	if report.Failed != 0 {
		for _, caseReport := range report.Cases {
			if caseReport.Passed {
				continue
			}
			t.Errorf("case %q failed: %v (pipeline error: %s)", caseReport.Name, caseReport.Failures, caseReport.Error)
		}
	}
	if report.Passed != len(cases) {
		t.Fatalf("passed = %d, want %d", report.Passed, len(cases))
	}
	if report.SchemaVersion != SchemaVersion {
		t.Fatalf("report schema version = %d, want %d", report.SchemaVersion, SchemaVersion)
	}
	for _, caseReport := range report.Cases {
		if caseReport.Metrics.Questions == 0 {
			t.Errorf("case %q measured no questions", caseReport.Name)
		}
		if caseReport.Metrics.TreeNodes == 0 {
			t.Errorf("case %q measured no tree nodes", caseReport.Name)
		}
		if caseReport.Metrics.Evidence == 0 {
			t.Errorf("case %q measured no evidence", caseReport.Name)
		}
		if !caseReport.Metrics.ContractValid {
			t.Errorf("case %q reports an invalid JSON contract", caseReport.Name)
		}
		if caseReport.Metrics.ModelCalls == 0 {
			t.Errorf("case %q measured no model calls", caseReport.Name)
		}
	}
	if report.TotalDurationMS < 0 {
		t.Fatalf("total duration = %d, want >= 0", report.TotalDurationMS)
	}
}

func TestRunCasePipelineError(t *testing.T) {
	tc := caseByName(t, "data-analyst-baseline")
	tc.Script.Analyst = agent.AnalystOutput{}
	report := RunCase(context.Background(), tc)
	if report.Passed {
		t.Fatal("case without analyst entities passed, want failure")
	}
	if report.Error == "" {
		t.Fatal("pipeline error is empty")
	}
	if !failuresContain(report.Failures, "pipeline") {
		t.Fatalf("failures = %v, want a pipeline failure", report.Failures)
	}
}

func TestRunCaseModelCallBudget(t *testing.T) {
	tc := caseByName(t, "data-analyst-baseline")
	tc.Expect.ModelCalls = intPtr(99)
	report := RunCase(context.Background(), tc)
	if report.Passed {
		t.Fatal("wrong model call budget passed")
	}
	if !failuresContain(report.Failures, "model calls") {
		t.Fatalf("failures = %v, want a model call failure", report.Failures)
	}
}

func TestCheckResultDetectsRegressions(t *testing.T) {
	baseline := caseByName(t, "data-analyst-baseline")
	result := runOne(t, baseline.Name)
	if failures := CheckResult(baseline, result); len(failures) != 0 {
		t.Fatalf("baseline case failed unexpectedly: %v", failures)
	}

	mutateExpect := func(change func(*Expect)) []string {
		mutated := baseline
		mutated.Expect = baseline.Expect
		change(&mutated.Expect)
		return CheckResult(mutated, result)
	}
	check := func(name, fragment string, failures []string) {
		t.Helper()
		if !failuresContain(failures, fragment) {
			t.Errorf("%s: failures = %v, want fragment %q", name, failures, fragment)
		}
	}

	check("status", "status =", mutateExpect(func(e *Expect) { e.Status = strPtr("failed") }))
	check("questions", "questions =", mutateExpect(func(e *Expect) { e.Questions = intPtr(1) }))
	check("clarifications", "clarifications =", mutateExpect(func(e *Expect) { e.Clarifications = intPtr(1) }))
	check("fallbacks", "fallbacks =", mutateExpect(func(e *Expect) { e.Fallbacks = intPtr(1) }))
	check("tree nodes", "tree nodes =", mutateExpect(func(e *Expect) { e.TreeNodes = intPtr(99) }))
	check("evidence", "tree evidence =", mutateExpect(func(e *Expect) { e.Evidence = intPtr(99) }))
	check("min evidence", "want at least", mutateExpect(func(e *Expect) { e.MinEvidencePerNode = intPtr(2) }))

	validateMutation := func(name, fragment string, change func(result *pipeline.Result)) []string {
		t.Helper()
		mutated := result
		deepCopyResult(&mutated, result)
		change(&mutated)
		failures := CheckResult(baseline, mutated)
		check(name, fragment, failures)
		return failures
	}

	validateMutation("multiple roots", "root nodes", func(mutated *pipeline.Result) {
		mutated.Synthesis.Tree[1].ParentID = nil
	})
	validateMutation("missing parent", "missing parent", func(mutated *pipeline.Result) {
		mutated.Synthesis.Tree[1].ParentID = strPtr("no-such-node")
	})
	validateMutation("invalid parent type", "cannot be a child of", func(mutated *pipeline.Result) {
		for i := range mutated.Synthesis.Tree {
			if mutated.Synthesis.Tree[i].Type == domain.NodeTask {
				mutated.Synthesis.Tree[i].ParentID = strPtr("tax-role-data-analyst")
			}
		}
	})
	validateMutation("node evidence", "evidence", func(mutated *pipeline.Result) {
		mutated.Synthesis.Tree[0].Evidence = nil
	})
	validateMutation("verbatim quote", "verbatim", func(mutated *pipeline.Result) {
		node := &mutated.Synthesis.Tree[0]
		node.Evidence[0].Quote = "этой фразы нет в транскрипте"
	})
	validateMutation("empty transcript", "transcript has", func(mutated *pipeline.Result) {
		mutated.Transcript = ""
	})
	validateMutation("dropped node type", "tree has no", func(mutated *pipeline.Result) {
		kept := make([]domain.Node, 0, len(mutated.Synthesis.Tree))
		for _, node := range mutated.Synthesis.Tree {
			if node.Type == domain.NodeAction {
				continue
			}
			kept = append(kept, node)
		}
		mutated.Synthesis.Tree = kept
	})
}

func TestCheckResultDetectsConflictRegressions(t *testing.T) {
	baseline := caseByName(t, "backend-hybrid-conflict")
	result := runOne(t, baseline.Name)
	if failures := CheckResult(baseline, result); len(failures) != 0 {
		t.Fatalf("conflict baseline failed unexpectedly: %v", failures)
	}
	mutated := baseline
	mutated.Expect = baseline.Expect
	mutated.Expect.ConflictWinners = []string{"vacancy"}
	if failures := CheckResult(mutated, result); !failuresContain(failures, "winner source") {
		t.Fatalf("failures = %v, want a conflict winner failure", failures)
	}

	wrongWinner := result
	wrongWinner.Synthesis.Conflicts = append([]domain.Conflict(nil), result.Synthesis.Conflicts...)
	wrongWinner.Synthesis.Conflicts[0].Winner = deterministicConflictValue(result.Synthesis.Conflicts[0].Values, domain.SourceVacancy)
	if failures := CheckResult(baseline, wrongWinner); !failuresContain(failures, "winner =") {
		t.Fatalf("failures = %v, want a conflict invariant failure", failures)
	}

	wrongCount := baseline
	wrongCount.Expect = baseline.Expect
	wrongCount.Expect.Conflicts = intPtr(0)
	if failures := CheckResult(wrongCount, result); !failuresContain(failures, "conflicts =") {
		t.Fatalf("failures = %v, want a conflict count failure", failures)
	}
}

func TestCheckResultDetectsContractViolation(t *testing.T) {
	baseline := caseByName(t, "data-analyst-baseline")
	result := runOne(t, baseline.Name)
	result.Synthesis.Tree = append([]domain.Node(nil), result.Synthesis.Tree...)
	result.Synthesis.Tree[0].Type = domain.NodeType("department")
	if failures := CheckResult(baseline, result); !failuresContain(failures, "schema") {
		t.Fatalf("failures = %v, want a JSON schema failure", failures)
	}
}

func TestLoadCasesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	base := caseByName(t, "data-analyst-baseline")
	first := base
	first.Name = "zz-copy-first"
	second := base
	second.Name = "aa-copy-second"
	writeCase := func(name string, tc Case) {
		t.Helper()
		data, err := json.Marshal(tc)
		if err != nil {
			t.Fatalf("marshal case: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write case: %v", err)
		}
	}
	writeCase("first.json", first)
	writeCase("second.json", second)

	cases, err := LoadCasesDir(dir)
	if err != nil {
		t.Fatalf("LoadCasesDir: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("loaded %d cases, want 2", len(cases))
	}
	if cases[0].Name != "zz-copy-first" || cases[1].Name != "aa-copy-second" {
		t.Fatalf("cases are not sorted by file name: %q, %q", cases[0].Name, cases[1].Name)
	}

	duplicateDir := t.TempDir()
	duplicate := base
	if err := os.WriteFile(filepath.Join(duplicateDir, "a.json"), mustMarshal(t, duplicate), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDir, "b.json"), mustMarshal(t, duplicate), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCasesDir(duplicateDir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate case names: err = %v, want duplicate error", err)
	}

	emptyDir := t.TempDir()
	if _, err := LoadCasesDir(emptyDir); err == nil {
		t.Fatal("empty cases dir: want error")
	}

	single, err := LoadCaseFile(filepath.Join(dir, "first.json"))
	if err != nil {
		t.Fatalf("LoadCaseFile: %v", err)
	}
	if single.Name != "zz-copy-first" {
		t.Fatalf("loaded case %q, want zz-copy-first", single.Name)
	}
	if _, err := LoadCaseFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing case file: want error")
	}
}

func TestLoadCaseRejectsBadFixtures(t *testing.T) {
	base := caseByName(t, "data-analyst-baseline")
	tests := map[string]string{
		"bad schema version": `{"schema_version": 2, "name": "x", "profession": "p"}`,
		"unknown field":      `{"schema_version": 1, "name": "x", "profession": "p", "surprise": true}`,
		"missing name":       `{"schema_version": 1, "profession": "p"}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.json")
			if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadCaseFile(path); err == nil {
				t.Fatalf("want error for %s", name)
			}
		})
	}
	base.SchemaVersion = SchemaVersion + 1
	if err := base.Validate(); err == nil {
		t.Fatal("validate accepted an unsupported schema version")
	}
}

func TestWriteTableAndJSON(t *testing.T) {
	cases := []Case{caseByName(t, "data-analyst-baseline"), caseByName(t, "backend-hybrid-conflict")}
	report := RunAll(context.Background(), cases)

	var table bytes.Buffer
	if err := WriteTable(&table, report); err != nil {
		t.Fatalf("WriteTable: %v", err)
	}
	for _, fragment := range []string{"CASE", "STATUS", "SUMMARY: 2/2 cases passed", "PASS"} {
		if !strings.Contains(table.String(), fragment) {
			t.Errorf("table does not contain %q:\n%s", fragment, table.String())
		}
	}
	if strings.Contains(table.String(), "FAILURES") {
		t.Errorf("passing report printed failures:\n%s", table.String())
	}

	var jsonReport bytes.Buffer
	if err := WriteJSON(&jsonReport, report); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(jsonReport.Bytes(), &decoded); err != nil {
		t.Fatalf("report JSON is invalid: %v", err)
	}
	if decoded.Passed != 2 || decoded.Failed != 0 || len(decoded.Cases) != 2 {
		t.Fatalf("decoded report = passed %d, failed %d, cases %d; want 2/0/2",
			decoded.Passed, decoded.Failed, len(decoded.Cases))
	}
	if decoded.SchemaVersion != SchemaVersion {
		t.Fatalf("report schema version = %d, want %d", decoded.SchemaVersion, SchemaVersion)
	}

	failing := caseByName(t, "data-analyst-baseline")
	failing.Expect.ModelCalls = intPtr(99)
	failingReport := RunAll(context.Background(), []Case{failing})
	var failingTable bytes.Buffer
	if err := WriteTable(&failingTable, failingReport); err != nil {
		t.Fatalf("WriteTable failing: %v", err)
	}
	for _, fragment := range []string{"FAIL", "FAILURES", "model calls =", "SUMMARY: 0/1 cases passed"} {
		if !strings.Contains(failingTable.String(), fragment) {
			t.Errorf("failing table does not contain %q:\n%s", fragment, failingTable.String())
		}
	}
	jsonReport.Reset()
	if err := WriteJSON(&jsonReport, failingReport); err != nil {
		t.Fatalf("WriteJSON failing: %v", err)
	}
	decoded = Report{}
	if err := json.Unmarshal(jsonReport.Bytes(), &decoded); err != nil {
		t.Fatalf("failing report JSON is invalid: %v", err)
	}
	if decoded.Failed != 1 || len(decoded.Cases[0].Failures) == 0 {
		t.Fatalf("failing report = %+v, want one failed case with failures", decoded)
	}

	var emptyTable bytes.Buffer
	if err := WriteTable(&emptyTable, Report{}); err != nil {
		t.Fatalf("WriteTable empty: %v", err)
	}
	if !strings.Contains(emptyTable.String(), "SUMMARY: 0/0 cases passed") {
		t.Errorf("empty report summary = %q", emptyTable.String())
	}
}

func TestRunEmbeddedGoldenSet(t *testing.T) {
	report, err := Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Failed != 0 {
		t.Fatalf("embedded golden set has failures: %+v", report.Cases)
	}
	if report.Passed != len(defaultCases(t)) {
		t.Fatalf("Run passed %d cases, want %d", report.Passed, len(defaultCases(t)))
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// deepCopyResult copies the slices a mutation test may edit so the shared
// baseline result is never modified between subtests.
func deepCopyResult(dst *pipeline.Result, src pipeline.Result) {
	*dst = src
	dst.Synthesis.Tree = append([]domain.Node(nil), src.Synthesis.Tree...)
	for i := range dst.Synthesis.Tree {
		node := &dst.Synthesis.Tree[i]
		node.Evidence = append([]domain.Evidence(nil), node.Evidence...)
	}
	dst.Synthesis.Conflicts = append([]domain.Conflict(nil), src.Synthesis.Conflicts...)
	dst.Session.Questions = append([]domain.Question(nil), src.Session.Questions...)
}

func deterministicConflictValue(values []domain.ConflictValue, source domain.SourceType) domain.ConflictValue {
	for _, value := range values {
		if value.SourceType == source {
			return value
		}
	}
	return values[0]
}
