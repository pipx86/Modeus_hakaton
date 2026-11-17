package agents

import (
	"context"
	"testing"

	"activity-descriptor/internal/llm"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type tracingFakeClient struct{}

func (tracingFakeClient) Chat(context.Context, []llm.Message) (string, error) {
	return "ok", nil
}

func TestTracedClientRecordsAgentSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	client := NewTracedClient(tracingFakeClient{}, Interviewer, "test-model", "low")
	answer, err := client.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	if err != nil || answer != "ok" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "agent.chat" {
		t.Fatalf("span name = %q", spans[0].Name())
	}
}

func TestTracedClientRequiresNext(t *testing.T) {
	if _, err := (&TracedClient{}).Chat(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil next")
	}
}
