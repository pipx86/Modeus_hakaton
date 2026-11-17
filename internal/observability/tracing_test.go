package observability

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStartFinishRecordsSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := Start(context.Background(), "agent.chat", attribute.String("agent", "analyst"))
	if ctx == nil || span == nil {
		t.Fatal("Start returned nil")
	}
	Finish(span, errors.New("provider down"))

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "agent.chat" {
		t.Fatalf("span name = %q", spans[0].Name())
	}
	found := false
	for _, attr := range spans[0].Attributes() {
		if attr.Key == "agent" && attr.Value.AsString() == "analyst" {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent attribute missing: %#v", spans[0].Attributes())
	}
}

func TestFinishNilSpan(t *testing.T) {
	Finish(nil, nil)
}
