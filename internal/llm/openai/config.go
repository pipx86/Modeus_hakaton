package openai

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the OpenRouter OpenAI-compatible endpoint.
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// DefaultTitle is sent in the optional X-Title header.
	DefaultTitle = "activity-descriptor"
	// DefaultTimeout is used when LLM_TIMEOUT_S is not set.
	DefaultTimeout = 60 * time.Second
)

// Config is an OpenAI-compatible provider configuration.
type Config struct {
	BaseURL     string
	APIKey      string
	Model       string
	Timeout     time.Duration
	HTTPReferer string
	Title       string

	// Optional request defaults. Zero values mean "not set".
	MaxTokens       int
	Temperature     *float64
	ReasoningEffort string
	ResponseFormat  json.RawMessage
}

// ConfigFromEnv builds Config from OPENROUTER_* variables and LLM_TIMEOUT_S.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL")), "/"),
		APIKey:      strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		Model:       strings.TrimSpace(os.Getenv("OPENROUTER_MODEL")),
		Timeout:     DefaultTimeout,
		HTTPReferer: strings.TrimSpace(os.Getenv("OPENROUTER_HTTP_REFERER")),
		Title:       strings.TrimSpace(os.Getenv("OPENROUTER_TITLE")),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("OPENROUTER_API_KEY is required")
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("OPENROUTER_MODEL is required")
	}
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}
	if raw := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_S")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return Config{}, fmt.Errorf("LLM_TIMEOUT_S must be a positive integer, got %q", raw)
		}
		cfg.Timeout = time.Duration(seconds) * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("LLM_MAX_TOKENS")); raw != "" {
		maxTokens, err := strconv.Atoi(raw)
		if err != nil || maxTokens <= 0 {
			return Config{}, fmt.Errorf("LLM_MAX_TOKENS must be a positive integer, got %q", raw)
		}
		cfg.MaxTokens = maxTokens
	}
	if raw := strings.TrimSpace(os.Getenv("LLM_TEMPERATURE")); raw != "" {
		temperature, err := strconv.ParseFloat(raw, 64)
		if err != nil || temperature < 0 || temperature > 2 {
			return Config{}, fmt.Errorf("LLM_TEMPERATURE must be between 0 and 2, got %q", raw)
		}
		cfg.Temperature = &temperature
	}
	cfg.ReasoningEffort = strings.TrimSpace(os.Getenv("LLM_REASONING_EFFORT"))
	return cfg, nil
}
