package ollama

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is used when OLLAMA_URL is not set.
	DefaultBaseURL = "http://localhost:11434"
	// DefaultTimeout is used when LLM_TIMEOUT_S is not set.
	DefaultTimeout = 60 * time.Second
)

// Config is the Ollama client configuration.
type Config struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

// ConfigFromEnv builds Config from OLLAMA_URL, OLLAMA_MODEL and LLM_TIMEOUT_S.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("OLLAMA_URL")), "/"),
		Model:   strings.TrimSpace(os.Getenv("OLLAMA_MODEL")),
		Timeout: DefaultTimeout,
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("OLLAMA_MODEL is required")
	}
	if raw := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_S")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return Config{}, fmt.Errorf("LLM_TIMEOUT_S must be a positive integer, got %q", raw)
		}
		cfg.Timeout = time.Duration(seconds) * time.Second
	}
	return cfg, nil
}
