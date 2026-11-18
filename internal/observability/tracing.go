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

// Setup installs a process-wide tracer provider and returns its shutdown
// function.
//
// Without OTEL_EXPORTER_OTLP_ENDPOINT the provider records spans locally and
// attaches no exporter (no network I/O, nothing to shut down over the wire).
// With an endpoint it attaches a batched OTLP/HTTP exporter; creating that
// client is local, so startup never waits for the collector. A malformed
// endpoint makes Setup return an error and leaves the global provider
// untouched, which lets main log a warning and keep serving without tracing.
//
// The returned shutdown function flushes pending spans and is safe to call
// more than once.
func Setup() (func(context.Context) error, error) {
	provider, err := newProvider(context.Background(), ConfigFromEnv())
	if err != nil {
		return nil, err
	}
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
