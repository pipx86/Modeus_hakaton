package agents

import (
	"os"
	"testing"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_MODEL", "")
	t.Setenv("LLM_MAX_CONCURRENCY", "")
	t.Setenv("LLM_REQUESTS_PER_MINUTE", "")
	t.Setenv("LLM_INTERVIEWER_FALLBACK_MODEL", "")
	t.Setenv("LLM_ANALYST_FALLBACK_MODEL", "")
	t.Setenv("LLM_SYNTHESIZER_FALLBACK_MODEL", "")
	// Ensure a stray secret file cannot affect this test.
	t.Setenv("LLM_API_KEY_FILE", os.DevNull)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("APIKey = %q", cfg.APIKey)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.MaxConcurrency != 3 {
		t.Fatalf("MaxConcurrency = %d", cfg.MaxConcurrency)
	}
	if cfg.Agents[Interviewer].Model != DefaultModel {
		t.Fatalf("interviewer model = %q", cfg.Agents[Interviewer].Model)
	}
	if cfg.Agents[Interviewer].Temperature != 0.4 {
		t.Fatalf("interviewer temperature = %v", cfg.Agents[Interviewer].Temperature)
	}
	if cfg.Agents[Analyst].MaxTokens != 8000 {
		t.Fatalf("analyst max tokens = %d", cfg.Agents[Analyst].MaxTokens)
	}
	// The synthesizer now performs a real (best-effort) model call to normalize
	// titles/descriptions, so it must get a valid default model and budget.
	if got := cfg.Agents[Synthesizer].Model; got != DefaultModel {
		t.Fatalf("synthesizer model = %q, want %q", got, DefaultModel)
	}
	if cfg.Agents[Synthesizer].MaxTokens != 4000 {
		t.Fatalf("synthesizer max tokens = %d", cfg.Agents[Synthesizer].MaxTokens)
	}
	for _, name := range Names() {
		if cfg.Agents[name].FallbackModel != "" {
			t.Fatalf("%s fallback model = %q, want disabled by default", name, cfg.Agents[name].FallbackModel)
		}
	}
}

func TestFromEnvFallbackModels(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_MODEL", "")
	t.Setenv("LLM_API_KEY_FILE", os.DevNull)
	t.Setenv("LLM_ANALYST_MODEL", "minimax-m3.1-flash")
	t.Setenv("LLM_ANALYST_FALLBACK_MODEL", "deepseek-v4.1-flash-1017")
	t.Setenv("LLM_INTERVIEWER_FALLBACK_MODEL", "backup-interviewer")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if got := cfg.Agents[Analyst].FallbackModel; got != "deepseek-v4.1-flash-1017" {
		t.Fatalf("analyst fallback model = %q", got)
	}
	if got := cfg.Agents[Analyst].Model; got != "minimax-m3.1-flash" {
		t.Fatalf("analyst primary model = %q, want the configured primary unchanged", got)
	}
	if got := cfg.Agents[Interviewer].FallbackModel; got != "backup-interviewer" {
		t.Fatalf("interviewer fallback model = %q", got)
	}
	if got := cfg.Agents[Synthesizer].FallbackModel; got != "" {
		t.Fatalf("synthesizer fallback model = %q, want disabled", got)
	}
}

func TestFromEnvDuplicateFallbackModelIsDisabled(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_API_KEY_FILE", os.DevNull)
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("LLM_ANALYST_MODEL", "minimax-m3.1-flash")
	t.Setenv("LLM_ANALYST_FALLBACK_MODEL", "minimax-m3.1-flash")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v, a duplicate fallback must not disable the provider", err)
	}
	if got := cfg.Agents[Analyst].FallbackModel; got != "" {
		t.Fatalf("analyst fallback model = %q, want disabled for a duplicate", got)
	}
	if got := cfg.Agents[Analyst].Model; got != "minimax-m3.1-flash" {
		t.Fatalf("analyst primary model = %q, want the configured primary unchanged", got)
	}
}

func TestFromEnvGenericProviderVariables(t *testing.T) {
	t.Setenv("LLM_API_KEY", "generic-key")
	t.Setenv("LLM_BASE_URL", "https://api.example.test/v1")
	t.Setenv("LLM_MODEL", "example-model")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_BASE_URL", "")
	t.Setenv("OPENROUTER_MODEL", "")
	t.Setenv("LLM_API_KEY_FILE", os.DevNull)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.APIKey != "generic-key" || cfg.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.Agents[Interviewer].Model != "example-model" {
		t.Fatalf("model = %q", cfg.Agents[Interviewer].Model)
	}
}

func TestFromEnvRejectsLegacyKeyForGenericBase(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_BASE_URL", "https://api.reformboss.com/v1")
	t.Setenv("OPENROUTER_API_KEY", "old-openrouter-key")
	t.Setenv("LLM_API_KEY_FILE", os.DevNull)
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error when OPENROUTER_API_KEY is used with LLM_BASE_URL")
	}
}

func TestFromEnvPerAgentOverrides(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_MODEL", "default-model")
	t.Setenv("LLM_INTERVIEWER_MODEL", "interviewer-model")
	t.Setenv("LLM_SYNTHESIZER_MODEL", "synthesizer-model")
	t.Setenv("LLM_INTERVIEWER_TEMPERATURE", "1.1")
	t.Setenv("LLM_INTERVIEWER_MAX_TOKENS", "123")
	t.Setenv("LLM_ANALYST_TIMEOUT_S", "42")
	t.Setenv("OPENROUTER_API_KEY_FILE", os.DevNull)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Agents[Interviewer].Model != "interviewer-model" {
		t.Fatalf("interviewer model = %q", cfg.Agents[Interviewer].Model)
	}
	if cfg.Agents[Interviewer].Temperature != 1.1 {
		t.Fatalf("interviewer temperature = %v", cfg.Agents[Interviewer].Temperature)
	}
	if cfg.Agents[Interviewer].MaxTokens != 123 {
		t.Fatalf("interviewer max tokens = %d", cfg.Agents[Interviewer].MaxTokens)
	}
	if cfg.Agents[Synthesizer].Model != "synthesizer-model" {
		t.Fatalf("synthesizer model = %q", cfg.Agents[Synthesizer].Model)
	}
	if cfg.Agents[Analyst].Timeout.Seconds() != 42 {
		t.Fatalf("analyst timeout = %s", cfg.Agents[Analyst].Timeout)
	}
}

func TestFromEnvMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY_FILE", os.DevNull)
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error when the API key is missing")
	}
}

func TestFromEnvInvalidConcurrency(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("LLM_MAX_CONCURRENCY", "0")
	t.Setenv("OPENROUTER_API_KEY_FILE", os.DevNull)
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error for zero concurrency")
	}
}

func TestFromEnvReadsGenericProviderKeyFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fixture-*.key")
	if err != nil {
		t.Fatal(err)
	}
	fixture := f.Name()
	if _, err := f.WriteString(fixture); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_API_KEY_FILE", fixture)
	t.Setenv("LLM_BASE_URL", "http://localhost/v1")
	t.Setenv("LLM_MODEL", "fixture")
	t.Setenv("OPENROUTER_API_KEY", "")
	cfg, err := FromEnv()
	if err != nil || cfg.APIKey != fixture {
		t.Fatalf("configured provider did not read its key file: %v", err)
	}
}
