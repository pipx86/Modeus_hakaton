// Command llm-smoke is a tiny manual test client for the Ollama package.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/ollama"
)

func main() {
	prompt := flag.String("prompt", "", "user prompt (required)")
	system := flag.String("system", "", "optional system prompt")
	baseURL := flag.String("url", "", "override OLLAMA_URL")
	model := flag.String("model", "", "override OLLAMA_MODEL")
	timeout := flag.Duration("timeout", 0, "override LLM_TIMEOUT_S, e.g. 30s")
	flag.Parse()

	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "error: -prompt is required")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := ollama.ConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}
	if *baseURL != "" {
		cfg.BaseURL = *baseURL
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *timeout > 0 {
		cfg.Timeout = *timeout
	}

	client, err := ollama.New(cfg)
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
}
