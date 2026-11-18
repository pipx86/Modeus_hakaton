package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"activity-descriptor/internal/llm/agents"
)

// clearProviderEnv removes every inherited provider variable so a test never
// silently uses a real key from the developer shell. The original values are
// restored when the test finishes.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "LLM_") && !strings.HasPrefix(key, "OPENROUTER_") {
			continue
		}
		keyCopy, valueCopy := key, value
		if err := os.Unsetenv(keyCopy); err != nil {
			t.Fatalf("unset %s: %v", keyCopy, err)
		}
		t.Cleanup(func() { _ = os.Setenv(keyCopy, valueCopy) })
	}
}

// unsetEnv removes one variable for the duration of a test.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	value, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	if had {
		t.Cleanup(func() { _ = os.Setenv(key, value) })
	}
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llm.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestLoadLLMEnvFile(t *testing.T) {
	unsetEnv(t, "LLM_LIVE_EVAL_TEST_PLAIN")
	unsetEnv(t, "LLM_LIVE_EVAL_TEST_QUOTED")
	unsetEnv(t, "LLM_LIVE_EVAL_TEST_EXPORT")
	unsetEnv(t, "LLM_LIVE_EVAL_TEST_PRECEDENCE")
	unsetEnv(t, "DATABASE_LIVE_EVAL_TEST")
	t.Setenv("LLM_LIVE_EVAL_TEST_PRECEDENCE", "from-env")

	path := writeEnvFile(t, strings.Join([]string{
		"# local provider configuration",
		"",
		"LLM_LIVE_EVAL_TEST_PLAIN=plain-value",
		`LLM_LIVE_EVAL_TEST_QUOTED="quoted value"`,
		"export LLM_LIVE_EVAL_TEST_EXPORT=exported-value",
		"LLM_LIVE_EVAL_TEST_PRECEDENCE=from-file",
		"DATABASE_LIVE_EVAL_TEST=should-not-load",
		"JWT_LIVE_EVAL_TEST=should-not-load",
	}, "\n"))

	loaded, ignored, err := loadLLMEnvFile(path)
	if err != nil {
		t.Fatalf("loadLLMEnvFile: %v", err)
	}
	if loaded != 3 {
		t.Errorf("loaded = %d, want 3", loaded)
	}
	if ignored != 3 {
		t.Errorf("ignored = %d, want 3 (one already set, two non-LLM)", ignored)
	}
	for key, want := range map[string]string{
		"LLM_LIVE_EVAL_TEST_PLAIN":      "plain-value",
		"LLM_LIVE_EVAL_TEST_QUOTED":     "quoted value",
		"LLM_LIVE_EVAL_TEST_EXPORT":     "exported-value",
		"LLM_LIVE_EVAL_TEST_PRECEDENCE": "from-env",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := os.Getenv("DATABASE_LIVE_EVAL_TEST"); got != "" {
		t.Errorf("non-LLM variable was loaded: %q", got)
	}
}

func TestLoadLLMEnvFileErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, _, err := loadLLMEnvFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
			t.Fatal("want an error for a missing env file")
		}
	})
	t.Run("malformed line", func(t *testing.T) {
		path := writeEnvFile(t, "LLM_OK=1\nthis line has no equals sign\n")
		if _, _, err := loadLLMEnvFile(path); err == nil ||
			!strings.Contains(err.Error(), "want KEY=VALUE") {
			t.Fatalf("error = %v, want a KEY=VALUE error", err)
		}
	})
	t.Run("invalid variable name", func(t *testing.T) {
		path := writeEnvFile(t, "LLM BAD NAME=1\n")
		if _, _, err := loadLLMEnvFile(path); err == nil ||
			!strings.Contains(err.Error(), "invalid variable name") {
			t.Fatalf("error = %v, want an invalid name error", err)
		}
	})
}

func TestRunDryRun(t *testing.T) {
	clearProviderEnv(t)
	const secret = "sk-live-eval-dry-run-secret"
	path := writeEnvFile(t, strings.Join([]string{
		"LLM_API_KEY=" + secret,
		"LLM_BASE_URL=https://example.invalid/v1",
		"LLM_INTERVIEWER_MODEL=model-interviewer",
		"LLM_ANALYST_MODEL=model-analyst",
		"LLM_ANALYST_FALLBACK_MODEL=model-analyst-fallback",
		"LLM_TIMEOUT_S=60",
	}, "\n"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-dry-run", "-env-file", path, "-case-timeout", "2m"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	output := stdout.String()
	for _, fragment := range []string{
		"env file:", "loaded 6 LLM_* variables",
		"LIVE AGENT PIPELINE EVAL", "https://example.invalid/v1",
		"model=model-interviewer", "model=model-analyst", "fallback=model-analyst-fallback",
		"synthesizer: deterministic", "dry run:", "planned cases:",
		"case timeout: 2m0s",
	} {
		if !strings.Contains(output, fragment) {
			t.Errorf("stdout does not contain %q:\n%s", fragment, output)
		}
	}
	if strings.Contains(output, secret) || strings.Contains(stderr.String(), secret) {
		t.Error("the API key was printed")
	}
}

func TestRunDryRunCaseFilter(t *testing.T) {
	clearProviderEnv(t)
	path := writeEnvFile(t, "LLM_API_KEY=dummy\nLLM_BASE_URL=https://example.invalid/v1\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-dry-run", "-env-file", path, "-cases", "data-analyst-baseline,data-analyst-baseline"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr: %s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "cases: 1") {
		t.Errorf("stdout = %s, want one selected case", output)
	}
	if strings.Contains(output, "data-analyst-standard-conflict") {
		t.Errorf("stdout = %s, want only the selected case", output)
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown case", []string{"-dry-run", "-cases", "nope"}},
		{"empty case list", []string{"-dry-run", "-cases", " , "}},
		{"non-positive timeout", []string{"-dry-run", "-case-timeout", "0s"}},
		{"unknown flag", []string{"-nonsense"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf("run returned %d, want 2; stderr: %s", code, stderr.String())
			}
		})
	}
}

func TestRunMissingProviderConfig(t *testing.T) {
	clearProviderEnv(t)
	path := writeEnvFile(t, "# no provider variables\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-dry-run", "-env-file", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1; stdout: %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "provider config") {
		t.Errorf("stderr = %q, want a provider config error", stderr.String())
	}
}

func TestProviderInfoDoesNotExposeKey(t *testing.T) {
	const secret = "sk-provider-info-secret"
	cfg := agents.Config{
		BaseURL:      "https://example.invalid/v1",
		APIKey:       secret,
		Agents:       map[agents.AgentName]agents.Spec{},
		DefaultModel: "model",
	}
	cfg.Agents[agents.Interviewer] = agents.Spec{Model: "model-interviewer"}
	cfg.Agents[agents.Analyst] = agents.Spec{Model: "model-analyst", FallbackModel: "model-fallback"}
	info := providerInfo(cfg)
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal provider info: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("provider info leaks the API key: %s", data)
	}
	if info.Models[string(agents.Interviewer)] != "model-interviewer" {
		t.Errorf("interviewer model = %q", info.Models[string(agents.Interviewer)])
	}
	if info.FallbackModels[string(agents.Analyst)] != "model-fallback" {
		t.Errorf("analyst fallback model = %q", info.FallbackModels[string(agents.Analyst)])
	}
}
