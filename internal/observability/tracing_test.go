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

// isolateTracing clears the OTLP environment and restores the global tracer
// provider after the test, so tests never depend on or pollute each other.
func isolateTracing(t *testing.T) {
	t.Helper()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")
	previous := otel.GetTracerProvider()
	t.Cleanup(func() {
		// Restoring the default placeholder provider to itself would make the
		// OTel global delegate log a warning; only restore when it changed.
		if otel.GetTracerProvider() != previous {
			otel.SetTracerProvider(previous)
		}
	})
}

func TestConfigFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")
		cfg := ConfigFromEnv()
		if cfg.Endpoint != "" {
			t.Fatalf("Endpoint = %q, want empty", cfg.Endpoint)
		}
		if cfg.Insecure {
			t.Fatal("Insecure = true, want false")
		}
	})

	t.Run("values", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "  http://collector:4318  ")
		t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
		cfg := ConfigFromEnv()
		if cfg.Endpoint != "http://collector:4318" {
			t.Fatalf("Endpoint = %q, want trimmed URL", cfg.Endpoint)
		}
		if !cfg.Insecure {
			t.Fatal("Insecure = false, want true")
		}
	})

	t.Run("insecure variants", func(t *testing.T) {
		tests := map[string]bool{
			"1":       true,
			"TRUE":    true,
			"t":       true,
			"false":   false,
			"0":       false,
			"":        false,
			"unknown": false,
		}
		for raw, want := range tests {
			t.Run(raw, func(t *testing.T) {
				t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", raw)
				if got := ConfigFromEnv().Insecure; got != want {
					t.Fatalf("Insecure(%q) = %v, want %v", raw, got, want)
				}
			})
		}
	})
}

func TestExporterOptions(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "host port", cfg: Config{Endpoint: "collector:4318"}},
		{name: "host port insecure", cfg: Config{Endpoint: "127.0.0.1:4318", Insecure: true}},
		{name: "http URL", cfg: Config{Endpoint: "http://collector:4318"}},
		{name: "https URL", cfg: Config{Endpoint: "https://collector:4318"}},
		{name: "URL with path", cfg: Config{Endpoint: "http://collector:4318/otlp/v1/traces"}},
		{name: "unsupported scheme", cfg: Config{Endpoint: "ftp://collector:4318"}, wantErr: true},
		{name: "missing host", cfg: Config{Endpoint: "http://"}, wantErr: true},
		{name: "missing scheme delimiter", cfg: Config{Endpoint: "collector 4318"}, wantErr: true},
		{name: "path without scheme", cfg: Config{Endpoint: "collector:4318/v1/traces"}, wantErr: true},
		{name: "unparsable URL", cfg: Config{Endpoint: "http://[::1"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := exporterOptions(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("exporterOptions(%+v) = nil error, want error", tt.cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("exporterOptions(%+v) error = %v", tt.cfg, err)
			}
			if len(options) == 0 {
				t.Fatalf("exporterOptions(%+v) returned no options", tt.cfg)
			}
		})
	}
}

func TestNewProviderRecordsSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider, err := newProvider(context.Background(), Config{}, sdktrace.WithSpanProcessor(recorder))
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	_, span := Start(context.Background(), "agent.chat", attribute.String("agent", "analyst"))
	if !span.IsRecording() {
		t.Fatal("span is not recording")
	}
	Finish(span, nil)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "agent.chat" {
		t.Fatalf("span name = %q, want agent.chat", spans[0].Name())
	}
	resourceAttributes := spans[0].Resource().Attributes()
	found := false
	for _, attr := range resourceAttributes {
		if attr.Key == "service.name" && attr.Value.AsString() == serviceName {
			found = true
		}
	}
	if !found {
		t.Fatalf("resource does not carry service.name=%q: %#v", serviceName, resourceAttributes)
	}
}

func TestSetupWithoutEndpoint(t *testing.T) {
	isolateTracing(t)

	shutdown, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown function")
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatalf("global tracer provider = %T, want *sdktrace.TracerProvider", otel.GetTracerProvider())
	}

	// Without an exporter the SDK still records spans; they are simply local.
	_, span := Start(context.Background(), "agent.chat")
	if !span.IsRecording() {
		t.Fatal("span is not recording")
	}
	Finish(span, errors.New("provider down"))

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	// Shutdown must be safe to call again (main defers it, tests may too).
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

func TestSetupWithEndpoint(t *testing.T) {
	isolateTracing(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	// Constructing the exporter is local: no collector is required and no
	// network call happens here.
	shutdown, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown function")
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	// No spans have ended, so shutdown flushes an empty batch and does not try
	// to reach the collector.
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestNewProviderHostPortEndpoint(t *testing.T) {
	isolateTracing(t)

	// A bare host:port endpoint is accepted as well; the URL form is the
	// standard OTel env syntax and is what the docs recommend.
	provider, err := newProvider(context.Background(), Config{Endpoint: "127.0.0.1:4318", Insecure: true})
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if provider == nil {
		t.Fatal("newProvider returned a nil provider")
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestSetupRejectsInvalidEndpoint(t *testing.T) {
	isolateTracing(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "ftp://collector:4318")

	before := otel.GetTracerProvider()
	shutdown, err := Setup()
	if err == nil {
		t.Fatal("Setup returned nil error for an unsupported endpoint scheme")
	}
	if shutdown != nil {
		t.Fatal("Setup returned a shutdown function with an error")
	}
	if otel.GetTracerProvider() != before {
		t.Fatal("Setup replaced the global tracer provider despite an error")
	}
}

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
