// Package agents builds one configured LLM client per product agent and runs
// their independent model calls through a shared, bounded parallel gate.
//
// The three product agents are interviewer, analyst and synthesizer. Each gets
// its own model name, temperature, token budget and timeout, while a single
// concurrency limit and request rate limit protect the free Nemotron tier from
// 429 storms.
package agents

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// AgentName identifies one of the three product agents.
type AgentName string

// Supported product agents.
const (
	Interviewer AgentName = "interviewer"
	Analyst     AgentName = "analyst"
	Synthesizer AgentName = "synthesizer"
)

// Names returns the supported agents in stable order.
func Names() []AgentName {
	return []AgentName{Interviewer, Analyst, Synthesizer}
}

// DefaultModel is the default OpenAI-compatible model used by the project.
const DefaultModel = "deepseek-v4.1-flash-1017"

// DefaultBaseURL is the default OpenAI-compatible provider endpoint.
const DefaultBaseURL = "https://api.reformboss.com/v1"

// Legacy OpenRouter defaults are used only when the legacy OPENROUTER_API_KEY
// is supplied without any generic LLM_* provider variable.
const (
	legacyDefaultModel   = "nvidia/nemotron-3-ultra-550b-a55b:free"
	legacyDefaultBaseURL = "https://openrouter.ai/api/v1"
)

// Spec is the per-agent model configuration.
type Spec struct {
	Model string
	// FallbackModel is an optional second model tried when the primary model
	// fails transiently (timeout, unavailable, rate limit, empty/invalid
	// provider response). Empty disables the fallback chain, keeping the
	// primary-only behaviour. It is read from LLM_<AGENT>_FALLBACK_MODEL.
	FallbackModel   string
	Temperature     float64
	MaxTokens       int
	ReasoningEffort string
	Timeout         time.Duration
}

// Config is the shared provider configuration plus one Spec per agent.
type Config struct {
	BaseURL           string
	APIKey            string
	DefaultModel      string
	Timeout           time.Duration
	MaxConcurrency    int
	RequestsPerMinute int
	RetryMax          int
	Agents            map[AgentName]Spec
}

// FromEnv loads the provider and per-agent configuration from environment
// variables. LLM_API_KEY is preferred; OPENROUTER_* names remain supported for
// backwards compatibility.
func FromEnv() (Config, error) {
	llmKey := firstEnv("LLM_API_KEY")
	llmBase := firstEnv("LLM_BASE_URL")
	llmModel := firstEnv("LLM_MODEL")
	legacyKey := firstEnv("OPENROUTER_API_KEY")
	legacyBase := firstEnv("OPENROUTER_BASE_URL")
	legacyModel := firstEnv("OPENROUTER_MODEL")

	var apiKey, baseURL, model string
	if llmKey != "" || llmBase != "" || llmModel != "" {
		// Generic provider mode. A legacy key must never be paired with a
		// custom base URL: that produces a misleading 401 at the provider.
		if llmKey == "" {
			llmKey = readKeyFile(firstEnv("LLM_API_KEY_FILE"))
		}
		if llmKey == "" {
			return Config{}, fmt.Errorf("LLM_API_KEY or LLM_API_KEY_FILE is required when LLM_BASE_URL or LLM_MODEL is set")
		}
		apiKey = llmKey
		baseURL = llmBase
		model = llmModel
		if baseURL == "" {
			baseURL = DefaultBaseURL
		}
		if model == "" {
			model = DefaultModel
		}
	} else {
		// Backwards-compatible OpenRouter mode.
		apiKey = legacyKey
		if apiKey == "" {
			apiKey = readSecretFile()
		}
		baseURL = legacyBase
		model = legacyModel
		if apiKey != "" && legacyKey != "" {
			if baseURL == "" {
				baseURL = legacyDefaultBaseURL
			}
			if model == "" {
				model = legacyDefaultModel
			}
		}
		if baseURL == "" {
			baseURL = DefaultBaseURL
		}
		if model == "" {
			model = DefaultModel
		}
	}
	if apiKey == "" {
		return Config{}, fmt.Errorf("LLM_API_KEY is required (set LLM_API_KEY, or put it in .secrets/reformboss.key)")
	}

	timeout, err := durationEnv("LLM_TIMEOUT_S", 300*time.Second)
	if err != nil {
		return Config{}, err
	}
	concurrency, err := intEnv("LLM_MAX_CONCURRENCY", 3)
	if err != nil {
		return Config{}, err
	}
	if concurrency < 1 || concurrency > 10 {
		return Config{}, fmt.Errorf("LLM_MAX_CONCURRENCY must be between 1 and 10, got %d", concurrency)
	}
	rpm, err := intEnv("LLM_REQUESTS_PER_MINUTE", 20)
	if err != nil {
		return Config{}, err
	}
	if rpm < 1 {
		return Config{}, fmt.Errorf("LLM_REQUESTS_PER_MINUTE must be positive, got %d", rpm)
	}
	retryMax, err := intEnv("LLM_RETRY_MAX", 3)
	if err != nil {
		return Config{}, err
	}
	if retryMax < 0 || retryMax > 10 {
		return Config{}, fmt.Errorf("LLM_RETRY_MAX must be between 0 and 10, got %d", retryMax)
	}

	cfg := Config{
		BaseURL:           strings.TrimRight(baseURL, "/"),
		APIKey:            apiKey,
		DefaultModel:      model,
		Timeout:           timeout,
		MaxConcurrency:    concurrency,
		RequestsPerMinute: rpm,
		RetryMax:          retryMax,
		Agents:            make(map[AgentName]Spec, len(Names())),
	}
	for _, name := range Names() {
		spec, err := specFromEnv(name, model, timeout)
		if err != nil {
			return Config{}, err
		}
		cfg.Agents[name] = spec
	}
	return cfg, nil
}

func specFromEnv(name AgentName, defaultModel string, defaultTimeout time.Duration) (Spec, error) {
	upper := strings.ToUpper(string(name))
	spec := Spec{
		Model:         getenv("LLM_"+upper+"_MODEL", defaultModel),
		FallbackModel: getenv("LLM_"+upper+"_FALLBACK_MODEL", ""),
		Timeout:       defaultTimeout,
		MaxTokens:     3000,
	}
	// Agent-specific defaults: the interviewer is short and creative, the
	// analyst is precise and long, the synthesizer is deterministic.
	switch name {
	case Interviewer:
		spec.Temperature = 0.4
		spec.MaxTokens = 1500
		spec.ReasoningEffort = "low"
	case Analyst:
		spec.Temperature = 0.2
		spec.MaxTokens = 8000
		spec.ReasoningEffort = "low"
	case Synthesizer:
		spec.Temperature = 0.1
		spec.MaxTokens = 4000
		spec.ReasoningEffort = "low"
	}

	rawTemp := strings.TrimSpace(os.Getenv("LLM_" + upper + "_TEMPERATURE"))
	if rawTemp != "" {
		value, err := strconv.ParseFloat(rawTemp, 64)
		if err != nil || value < 0 || value > 2 {
			return Spec{}, fmt.Errorf("LLM_%s_TEMPERATURE must be between 0 and 2, got %q", upper, rawTemp)
		}
		spec.Temperature = value
	}
	rawTokens := strings.TrimSpace(os.Getenv("LLM_" + upper + "_MAX_TOKENS"))
	if rawTokens != "" {
		value, err := strconv.Atoi(rawTokens)
		if err != nil || value <= 0 {
			return Spec{}, fmt.Errorf("LLM_%s_MAX_TOKENS must be a positive integer, got %q", upper, rawTokens)
		}
		spec.MaxTokens = value
	}
	rawEffort := strings.TrimSpace(os.Getenv("LLM_" + upper + "_REASONING_EFFORT"))
	if rawEffort != "" {
		effort := strings.ToLower(rawEffort)
		if effort != "low" && effort != "medium" && effort != "high" {
			return Spec{}, fmt.Errorf("LLM_%s_REASONING_EFFORT must be low, medium or high, got %q", upper, rawEffort)
		}
		spec.ReasoningEffort = effort
	}
	if rawTimeout := strings.TrimSpace(os.Getenv("LLM_" + upper + "_TIMEOUT_S")); rawTimeout != "" {
		seconds, err := strconv.Atoi(rawTimeout)
		if err != nil || seconds <= 0 {
			return Spec{}, fmt.Errorf("LLM_%s_TIMEOUT_S must be a positive integer, got %q", upper, rawTimeout)
		}
		spec.Timeout = time.Duration(seconds) * time.Second
	}
	if spec.Model == "" {
		return Spec{}, fmt.Errorf("LLM_%s_MODEL is empty", upper)
	}
	if spec.FallbackModel == spec.Model {
		// A duplicate fallback would call the same model twice. Treat it as
		// disabled: refusing the whole provider configuration over a harmless
		// duplicate would take the LLM out of service on demo day.
		spec.FallbackModel = ""
	}
	return spec, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func readSecretFile() string {
	path := firstEnv("LLM_API_KEY_FILE", "OPENROUTER_API_KEY_FILE")
	if path == "" {
		path = ".secrets/reformboss.key"
	}
	return readKeyFile(path)
}

func readKeyFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	key := strings.TrimSpace(string(data))
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n") {
		return ""
	}
	return key
}

func intEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return value, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer number of seconds, got %q", key, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}
