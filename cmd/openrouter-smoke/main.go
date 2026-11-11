// Command openrouter-smoke is a manual test client for the OpenRouter provider.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/openai"
	"activity-descriptor/internal/llm/validate"
)

func main() {
	prompt := flag.String("prompt", "", "user prompt (required)")
	system := flag.String("system", "", "optional system prompt")
	baseURL := flag.String("url", "", "override OPENROUTER_BASE_URL")
	model := flag.String("model", "", "override OPENROUTER_MODEL")
	timeout := flag.Duration("timeout", 0, "override timeout, e.g. 30s")
	maxTokens := flag.Int("max-tokens", 0, "limit generated tokens")
	temperature := flag.Float64("temperature", -1, "sampling temperature, e.g. 0.2")
	reasoning := flag.String("reasoning", "", "reasoning effort: low, medium or high")
	jsonMode := flag.Bool("json", false, "request JSON object output")
	schemaPath := flag.String("schema", "", "path to JSON schema for response_format")
	flag.Parse()

	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "error: -prompt is required")
		flag.Usage()
		os.Exit(2)
	}

	if *model != "" {
		_ = os.Setenv("OPENROUTER_MODEL", *model)
	}
	if *baseURL != "" {
		_ = os.Setenv("OPENROUTER_BASE_URL", *baseURL)
	}
	cfg, err := openai.ConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}
	if *timeout > 0 {
		cfg.Timeout = *timeout
	}
	if *maxTokens > 0 {
		cfg.MaxTokens = *maxTokens
	}
	if *temperature >= 0 {
		cfg.Temperature = temperature
	}
	if *reasoning != "" {
		cfg.ReasoningEffort = *reasoning
	}
	format, err := buildResponseFormat(*schemaPath, *jsonMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "response format error: %v\n", err)
		os.Exit(2)
	}
	cfg.ResponseFormat = format

	client, err := openai.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "client error: %v\n", err)
		os.Exit(2)
	}

	messages := make([]llm.Message, 0, 2)
	if strings.TrimSpace(*system) != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: *system})
	}
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: *prompt})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	start := time.Now()
	answer, err := client.Chat(ctx, messages)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error after %s: %v\n", elapsed.Round(time.Millisecond), err)
		os.Exit(1)
	}

	fmt.Printf("model:    %s\nurl:      %s\nduration: %s\n\n%s\n", cfg.Model, cfg.BaseURL, elapsed.Round(time.Millisecond), answer)

	if *jsonMode || *schemaPath != "" {
		if err := validate.JSON([]byte(answer)); err != nil {
			fmt.Fprintf(os.Stderr, "response validation error: %v\n", err)
			os.Exit(1)
		}
	}
	if *schemaPath != "" {
		schema, err := os.ReadFile(*schemaPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "schema read error: %v\n", err)
			os.Exit(1)
		}
		if err := validate.Schema([]byte(answer), schema); err != nil {
			fmt.Fprintf(os.Stderr, "response schema error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "response matches schema")
	}
}

func buildResponseFormat(schemaPath string, jsonMode bool) (json.RawMessage, error) {
	if schemaPath == "" {
		if jsonMode {
			return json.RawMessage(`{"type":"json_object"}`), nil
		}
		return nil, nil
	}

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(schema, &document); err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %w", err)
	}
	wrapper := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "response",
			"schema": document,
		},
	}
	return json.Marshal(wrapper)
}
