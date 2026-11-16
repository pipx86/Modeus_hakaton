package metrics

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"activity-descriptor/internal/llm"
)

func TestRegistryWritePrometheus(t *testing.T) {
	registry := New()
	registry.ObserveCall("analyst", "nemotron", "low", 200*time.Millisecond, nil)
	registry.ObserveCall("analyst", "nemotron", "low", 2*time.Second, llm.NewDomainError(
		llm.ErrorRateLimit, "openai", 429, true, errors.New("slow down"),
	))
	registry.ObserveUsage("analyst", llm.Usage{
		PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150, Model: "nemotron",
	})

	var buf bytes.Buffer
	if err := registry.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`activity_descriptor_agent_calls_total{agent="analyst",model="nemotron",status="ok",code="ok"} 1`,
		`activity_descriptor_agent_calls_total{agent="analyst",model="nemotron",status="error",code="rate_limit"} 1`,
		`activity_descriptor_agent_errors_total{agent="analyst",model="nemotron",code="rate_limit"} 1`,
		`activity_descriptor_agent_tokens_total{agent="analyst",model="nemotron",direction="input"} 120`,
		`activity_descriptor_agent_tokens_total{agent="analyst",model="nemotron",direction="output"} 30`,
		`activity_descriptor_agent_reasoning_total{agent="analyst",reasoning="low"} 2`,
		`activity_descriptor_agent_latency_seconds_count{agent="analyst",model="nemotron"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestNilRegistryIsSafe(t *testing.T) {
	var registry *Registry
	registry.ObserveCall("a", "m", "low", time.Second, nil)
	registry.ObserveUsage("a", llm.Usage{})
	var buf bytes.Buffer
	if err := registry.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
}
