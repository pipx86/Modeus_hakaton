package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/llm"
)

// clearLLMEnv blanks every provider-related environment variable for the
// duration of the test. Dry-run tests stay hermetic and can never pick up a
// real key from the developer machine or CI.
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "LLM_") || strings.HasPrefix(name, "OPENROUTER_") {
			t.Setenv(name, "")
		}
	}
}

func TestParseModelList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "empty", raw: "   ", want: nil},
		{name: "single", raw: "model-a", want: []string{"model-a"}},
		{name: "multiple", raw: "model-a, model-b ,model-c", want: []string{"model-a", "model-b", "model-c"}},
		{name: "duplicates removed", raw: "model-a,model-b,model-a", want: []string{"model-a", "model-b"}},
		{name: "empty item", raw: "model-a,,model-b", wantErr: true},
		{name: "trailing comma", raw: "model-a,", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseModelList(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseModelList(%q) = %v, want error", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseModelList(%q): %v", test.raw, err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("parseModelList(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestParseFloatList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []float64
		wantErr bool
	}{
		{name: "empty", raw: " ", want: nil},
		{name: "single", raw: "0.2", want: []float64{0.2}},
		{name: "multiple", raw: "0.2, 0.4", want: []float64{0.2, 0.4}},
		{name: "zero and upper bound", raw: "0,2", want: []float64{0, 2}},
		{name: "duplicates removed", raw: "0.4,0.4,0.2", want: []float64{0.4, 0.2}},
		{name: "not a number", raw: "warm", wantErr: true},
		{name: "negative", raw: "-0.1", wantErr: true},
		{name: "above range", raw: "2.5", wantErr: true},
		{name: "NaN", raw: "NaN", wantErr: true},
		{name: "Inf", raw: "Inf", wantErr: true},
		{name: "empty item", raw: "0.2,,0.4", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFloatList(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseFloatList(%q) = %v, want error", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFloatList(%q): %v", test.raw, err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("parseFloatList(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestParseIntList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []int
		wantErr bool
	}{
		{name: "empty", raw: "", want: nil},
		{name: "single", raw: "1500", want: []int{1500}},
		{name: "multiple", raw: "1500, 5000", want: []int{1500, 5000}},
		{name: "duplicates removed", raw: "1500,1500", want: []int{1500}},
		{name: "not an integer", raw: "1500.5", wantErr: true},
		{name: "zero", raw: "0", wantErr: true},
		{name: "negative", raw: "-10", wantErr: true},
		{name: "empty item", raw: "1500,", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseIntList(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseIntList(%q) = %v, want error", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIntList(%q): %v", test.raw, err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("parseIntList(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestBuildPlan(t *testing.T) {
	plan := buildPlan(
		[]string{"model-a", "model-b"},
		[]float64{0, 0.4},
		[]int{1000, 2000},
	)
	if len(plan) != 8 {
		t.Fatalf("len(plan) = %d, want 8", len(plan))
	}
	want := []combination{
		{Model: "model-a", Temperature: 0, MaxTokens: 1000},
		{Model: "model-a", Temperature: 0, MaxTokens: 2000},
		{Model: "model-a", Temperature: 0.4, MaxTokens: 1000},
		{Model: "model-a", Temperature: 0.4, MaxTokens: 2000},
		{Model: "model-b", Temperature: 0, MaxTokens: 1000},
		{Model: "model-b", Temperature: 0, MaxTokens: 2000},
		{Model: "model-b", Temperature: 0.4, MaxTokens: 1000},
		{Model: "model-b", Temperature: 0.4, MaxTokens: 2000},
	}
	if !slices.Equal(plan, want) {
		t.Fatalf("plan = %+v, want %+v", plan, want)
	}
}

func TestSummarizeAndExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		cells      []cellResult
		strict     bool
		wantExit   int
		wantFailed int
		wantAll    bool
	}{
		{
			name:       "all failed",
			cells:      []cellResult{{Success: false}, {Success: false}},
			wantExit:   exitFailure,
			wantFailed: 2,
			wantAll:    true,
		},
		{
			name:       "partial failure is not fatal by default",
			cells:      []cellResult{{Success: true, JSONValid: true}, {Success: false}},
			wantExit:   exitOK,
			wantFailed: 1,
		},
		{
			name:       "strict rejects partial failure",
			cells:      []cellResult{{Success: true, JSONValid: true}, {Success: false}},
			strict:     true,
			wantExit:   exitFailure,
			wantFailed: 1,
		},
		{
			name:     "strict rejects invalid json",
			cells:    []cellResult{{Success: true, JSONValid: true}, {Success: true, JSONValid: false}},
			strict:   true,
			wantExit: exitFailure,
		},
		{
			name:     "valid json is not verified in default mode",
			cells:    []cellResult{{Success: true, JSONValid: false}},
			wantExit: exitOK,
		},
		{
			name:     "all success",
			cells:    []cellResult{{Success: true, JSONValid: true}, {Success: true, JSONValid: true}},
			strict:   true,
			wantExit: exitOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := summarize(test.cells)
			if summary.Total != len(test.cells) || summary.Failed != test.wantFailed || summary.AllFailed != test.wantAll {
				t.Fatalf("summary = %+v, want failed=%d all=%v", summary, test.wantFailed, test.wantAll)
			}
			if got := exitCodeFor(summary, test.strict); got != test.wantExit {
				t.Fatalf("exitCodeFor(%+v, %v) = %d, want %d", summary, test.strict, got, test.wantExit)
			}
		})
	}
}

func TestSummarizeAverages(t *testing.T) {
	summary := summarize([]cellResult{
		{Success: true, JSONValid: true, DurationMS: 100, TotalTokens: 10},
		{Success: false, DurationMS: 300, TotalTokens: 0},
	})
	if summary.AvgDurationMS != 200 || summary.TotalTokens != 10 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestBuildMessages(t *testing.T) {
	for _, kind := range []agent.Kind{agent.KindInterviewer, agent.KindAnalyst} {
		t.Run(string(kind), func(t *testing.T) {
			messages, doc, err := buildMessages(kind)
			if err != nil {
				t.Fatalf("buildMessages(%q): %v", kind, err)
			}
			if len(messages) != 3 {
				t.Fatalf("len(messages) = %d, want 3", len(messages))
			}
			if messages[0].Role != llm.RoleSystem || strings.TrimSpace(messages[0].Content) == "" {
				t.Fatalf("messages[0] is not a non-empty system prompt")
			}
			if !strings.Contains(messages[1].Content, "# Examples") {
				t.Fatalf("messages[1] does not carry the few-shot examples")
			}
			last := messages[2]
			if last.Role != llm.RoleUser || !strings.Contains(last.Content, string(kind)+".output.schema.json") {
				t.Fatalf("messages[2] does not point at the %s schema: %q", kind, last.Content)
			}
			if !strings.Contains(last.Content, "Аналитик данных") {
				t.Fatalf("messages[2] does not contain the realistic profile input")
			}
			if doc.Version == "" || doc.SHA256 == "" {
				t.Fatalf("prompt document lacks version/hash: %+v", doc)
			}
		})
	}

	if _, _, err := buildMessages(agent.KindSynthesizer); err == nil {
		t.Fatal("buildMessages(synthesizer) = nil error, want unsupported-agent error")
	}
}

// TestRunDryRunPlansMatrix is the main no-network test: it checks the planned
// matrix, the effective settings and that a live key is not required.
func TestRunDryRunPlansMatrix(t *testing.T) {
	clearLLMEnv(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-dry-run",
		"-agent", "analyst",
		"-models", "model-a,model-b",
		"-temperatures", "0.1,0.2",
		"-max-tokens", "100,200",
		"-repeat", "2",
		"-timeout", "5s",
	}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}
	out := stdout.String()
	wants := []string{
		"dry-run (no network calls)",
		"agent:           analyst",
		"plan:            8 settings x 2 repeats = 16 requests",
		"planned matrix (8 settings x 2 repeats = 16 requests, no network calls)",
		"model=model-a temperature=0.10 max_tokens=100 timeout=5s",
		"model=model-b temperature=0.20 max_tokens=200 timeout=5s",
		"api key:         not configured (dry-run only)",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, dryRunPlaceholderKey) {
		t.Fatalf("stdout leaks the dry-run placeholder:\n%s", out)
	}
	if !strings.Contains(stderr.String(), "LLM_API_KEY is not set") {
		t.Fatalf("stderr does not explain the missing key:\n%s", stderr.String())
	}
}

// TestRunDryRunNeverPrintsAPIKey checks the key-redaction rule on the only path
// tests are allowed to exercise without a provider.
func TestRunDryRunNeverPrintsAPIKey(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("LLM_API_KEY", "sk-test-super-secret")
	t.Setenv("LLM_BASE_URL", "https://provider.example.invalid/v1")
	t.Setenv("LLM_MODEL", "matrix-model")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-dry-run"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "sk-test-super-secret") {
		t.Fatalf("output leaks the API key:\n%s", combined)
	}
	if !strings.Contains(stdout.String(), "api key:         configured (hidden)") {
		t.Fatalf("stdout does not report the hidden key:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "provider:        https://provider.example.invalid/v1") {
		t.Fatalf("stdout does not show the configured provider:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "model=matrix-model") {
		t.Fatalf("stdout does not use the configured model:\n%s", stdout.String())
	}
}

// TestRunDryRunWritesReport checks that -out produces the reproducible JSON
// artifact: effective settings, prompt fingerprint and the full planned matrix.
func TestRunDryRunWritesReport(t *testing.T) {
	clearLLMEnv(t)
	path := filepath.Join(t.TempDir(), "plan.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-dry-run", "-out", path}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v\n%s", err, data)
	}
	if !rep.DryRun {
		t.Fatal("report.DryRun = false, want true")
	}
	if rep.Agent != string(agent.KindInterviewer) || rep.Contract != agent.ContractID {
		t.Fatalf("report agent/contract = %q/%q", rep.Agent, rep.Contract)
	}
	if rep.PromptVersion == "" || rep.PromptSHA256 == "" {
		t.Fatalf("report lacks prompt fingerprint: %+v", rep)
	}
	if rep.APIKeyConfigured {
		t.Fatal("report.APIKeyConfigured = true without a key")
	}
	if rep.PlannedRequests != len(rep.Plan)*rep.Repeats || rep.PlannedRequests != 2 {
		t.Fatalf("report planned requests = %d (plan=%d repeats=%d)", rep.PlannedRequests, len(rep.Plan), rep.Repeats)
	}
	if _, err := time.Parse(time.RFC3339, rep.StartedAt); err != nil {
		t.Fatalf("report.StartedAt = %q, want RFC3339 timestamp: %v", rep.StartedAt, err)
	}
	if len(rep.Cells) != 0 || rep.Summary != nil {
		t.Fatalf("dry-run report must not contain measured cells: %+v", rep)
	}
	wantTemperatures := []float64{0.2, 0.4}
	gotTemperatures := make([]float64, 0, len(rep.Plan))
	for _, combo := range rep.Plan {
		gotTemperatures = append(gotTemperatures, combo.Temperature)
		if combo.Model == "" || combo.MaxTokens <= 0 {
			t.Fatalf("incomplete plan entry: %+v", combo)
		}
	}
	if !slices.Equal(gotTemperatures, wantTemperatures) {
		t.Fatalf("planned temperatures = %v, want %v", gotTemperatures, wantTemperatures)
	}
	if !strings.Contains(stdout.String(), "report: "+path) {
		t.Fatalf("stdout does not mention the report path:\n%s", stdout.String())
	}
}

// TestRunLiveWithoutKeyFailsClearly checks the live-mode guard: without a key
// the command exits during configuration loading and never reaches the network.
func TestRunLiveWithoutKeyFailsClearly(t *testing.T) {
	clearLLMEnv(t)

	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "LLM_API_KEY is required") {
		t.Fatalf("stderr does not explain the missing key:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "-dry-run needs no key") {
		t.Fatalf("stderr does not point at the dry-run option:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty on a live configuration error, got:\n%s", stdout.String())
	}
}

func TestRunRejectsInvalidFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unsupported agent", args: []string{"-agent", "synthesizer"}},
		{name: "unknown agent", args: []string{"-agent", "unknown"}},
		{name: "zero repeat", args: []string{"-repeat", "0"}},
		{name: "negative repeat", args: []string{"-repeat", "-1"}},
		{name: "zero timeout", args: []string{"-timeout", "0s"}},
		{name: "invalid temperature", args: []string{"-temperatures", "warm"}},
		{name: "temperature above range", args: []string{"-temperatures", "3"}},
		{name: "invalid max tokens", args: []string{"-max-tokens", "0"}},
		{name: "empty model item", args: []string{"-models", "a,,b"}},
		{name: "unexpected argument", args: []string{"extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearLLMEnv(t)
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("run(%v) exit code = %d, want %d", test.args, code, exitUsage)
			}
		})
	}
}

func TestPrintTable(t *testing.T) {
	cells := []cellResult{
		{
			Model:       "model-a",
			Temperature: 0.2,
			MaxTokens:   1200,
			Success:     true,
			JSONValid:   true,
			DurationMS:  345,
			TotalTokens: 678,
			Preview:     `{"question":{"id":"q","level":"role","text":"..."}}`,
		},
		{
			Model:       "model-b",
			Temperature: 0.4,
			MaxTokens:   900,
			Success:     false,
			DurationMS:  12,
			Error:       "openai: chat request: rate limited",
		},
	}
	var out bytes.Buffer
	printTable(&out, cells)
	table := out.String()
	for _, want := range []string{
		"MODEL", "TEMP", "MAX_TOKENS", "SUCCESS", "JSON", "DURATION_MS",
		"TOKENS", "ERROR", "PREVIEW", "model-a", "model-b",
		"rate limited", "345", "678",
	} {
		if !strings.Contains(table, want) {
			t.Fatalf("table does not contain %q:\n%s", want, table)
		}
	}
}

func TestPrintProgress(t *testing.T) {
	var out bytes.Buffer
	printProgress(&out, 2, 4, cellResult{
		Model:       "model-a",
		Temperature: 0.2,
		MaxTokens:   1200,
		Repeat:      1,
		Success:     false,
		DurationMS:  50,
		Error:       "boom",
	})
	got := out.String()
	for _, want := range []string{"[2/4]", "model=model-a", "temperature=0.20", "max_tokens=1200", "success=false", "duration=50ms", `error="boom"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress line does not contain %q: %s", want, got)
		}
	}
}

func TestRedact(t *testing.T) {
	got := redact("provider rejected key sk-abc-123", "sk-abc-123")
	if strings.Contains(got, "sk-abc-123") {
		t.Fatalf("redact() leaks the key: %s", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("redact() = %q, want [redacted] marker", got)
	}
	if got := redact("plain error", ""); got != "plain error" {
		t.Fatalf("redact(no key) = %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("  short  ", 10); got != "short" {
		t.Fatalf("truncateRunes(short) = %q", got)
	}
	long := strings.Repeat("я", 50)
	got := truncateRunes(long, 10)
	if runes := []rune(got); len(runes) != 11 || runes[10] != '…' {
		t.Fatalf("truncateRunes(long) = %q (len %d), want 10 runes + ellipsis", got, len(runes))
	}
	if got := truncateRunes("", 10); got != "" {
		t.Fatalf("truncateRunes(empty) = %q", got)
	}
}

func TestOneLine(t *testing.T) {
	got := oneLine("line one\nline two\tend\r\n")
	if got != "line one line two end" {
		t.Fatalf("oneLine() = %q", got)
	}
}
