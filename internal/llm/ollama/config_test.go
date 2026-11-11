package ollama

import (
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("OLLAMA_URL", "http://example.test:11434/")
	t.Setenv("OLLAMA_MODEL", "test-model")
	t.Setenv("LLM_TIMEOUT_S", "15")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.BaseURL != "http://example.test:11434" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Model != "test-model" {
		t.Fatalf("Model = %q", cfg.Model)
	}
	if cfg.Timeout != 15*time.Second {
		t.Fatalf("Timeout = %v", cfg.Timeout)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("OLLAMA_MODEL", "test-model")
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
}

func TestConfigFromEnvRequiresModel(t *testing.T) {
	t.Setenv("OLLAMA_MODEL", "")

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error for empty OLLAMA_MODEL")
	}
}

func TestConfigFromEnvRejectsBadTimeout(t *testing.T) {
	t.Setenv("OLLAMA_MODEL", "test-model")
	t.Setenv("LLM_TIMEOUT_S", "0")

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error for LLM_TIMEOUT_S=0")
	}
}
