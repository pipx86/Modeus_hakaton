package observability

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewProvider creates an SDK tracer provider. A caller may add a span exporter
// through trace.WithBatcher when an OTLP collector is available.
func NewProvider(options ...sdktrace.TracerProviderOption) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(options...)
}
