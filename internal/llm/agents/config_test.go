package agents

import (
	"os"
	"testing"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_MODEL", "")
	t.Setenv("LLM_MAX_CONCURRENCY", "")
	t.Setenv("LLM_REQUESTS_PER_MINUTE", "")
	// Ensure a stray secret file cannot affect this test.
	t.Setenv("OPENROUTER_API_KEY_FILE", os.DevNull)

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
	if cfg.Agents[Analyst].MaxTokens != 5000 {
		t.Fatalf("analyst max tokens = %d", cfg.Agents[Analyst].MaxTokens)
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

func TestFromEnvPerAgentOverrides(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_MODEL", "default-model")
	t.Setenv("LLM_INTERVIEWER_MODEL", "interviewer-model")
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
