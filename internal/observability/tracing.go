// Package observability configures OpenTelemetry tracing for agent calls.
package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "activity-descriptor/agents"

// Setup installs a process-wide tracer provider. Exporters can be attached by
// tests or by a future OTLP wiring; without one, spans are still sampled and
// recorded by the SDK but are not sent over the network.
func Setup() (func(context.Context) error, error) {
	provider := NewProvider()
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

// Start begins an agent span.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name, trace.WithAttributes(attrs...))
}

// Finish records an error and ends the span.
func Finish(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}
