package openai

import (
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("OPENROUTER_MODEL", "qwen/qwen3-32b")
	t.Setenv("OPENROUTER_BASE_URL", "https://example.test/api/v1/")
	t.Setenv("OPENROUTER_HTTP_REFERER", "https://example.test")
	t.Setenv("OPENROUTER_TITLE", "test-app")
	t.Setenv("LLM_TIMEOUT_S", "15")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.BaseURL != "https://example.test/api/v1" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Model != "qwen/qwen3-32b" {
		t.Fatalf("Model = %q", cfg.Model)
	}
	if cfg.Timeout != 15*time.Second {
		t.Fatalf("Timeout = %v", cfg.Timeout)
	}
	if cfg.HTTPReferer != "https://example.test" || cfg.Title != "test-app" {
		t.Fatalf("headers config = %#v", cfg)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("OPENROUTER_MODEL", "m")
	t.Setenv("OPENROUTER_BASE_URL", "")
	t.Setenv("OPENROUTER_TITLE", "")
	t.Setenv("LLM_TIMEOUT_S", "")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %v, want %v", cfg.Timeout, DefaultTimeout)
	}
	if cfg.Title != DefaultTitle {
		t.Fatalf("Title = %q, want %q", cfg.Title, DefaultTitle)
	}
}

func TestConfigFromEnvRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_MODEL", "m")

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error for empty OPENROUTER_API_KEY")
	}
}

func TestConfigFromEnvRequiresModel(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("OPENROUTER_MODEL", "")

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error for empty OPENROUTER_MODEL")
	}
}

func TestConfigFromEnvRequestDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "key")
	t.Setenv("OPENROUTER_MODEL", "m")
	t.Setenv("LLM_MAX_TOKENS", "512")
	t.Setenv("LLM_TEMPERATURE", "0.2")
	t.Setenv("LLM_REASONING_EFFORT", "low")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.MaxTokens != 512 {
		t.Fatalf("MaxTokens = %d", cfg.MaxTokens)
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.2 {
		t.Fatalf("Temperature = %v", cfg.Temperature)
	}
	if cfg.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q", cfg.ReasoningEffort)
	}
}
