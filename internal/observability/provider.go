package observability

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

const (
	// serviceName is reported to the collector as the OTLP resource
	// service.name attribute.
	serviceName = "activity-descriptor"
	// exporterTimeout bounds one OTLP/HTTP export request. It keeps provider
	// shutdown bounded when the collector is slow or unreachable.
	exporterTimeout = 5 * time.Second
)

// Config controls the optional OTLP/HTTP span exporter.
//
// An empty Endpoint keeps spans local: the SDK still records them, but no
// exporter is attached and nothing is sent over the network.
type Config struct {
	// Endpoint is the OTLP/HTTP collector target. Both a URL
	// ("http://localhost:4318") and a bare host:port ("localhost:4318") are
	// accepted. Empty disables exporting.
	Endpoint string
	// Insecure sends spans over plain HTTP. A URL endpoint already implies the
	// scheme; this flag is needed for plain-HTTP host:port endpoints.
	Insecure bool
}

// ConfigFromEnv reads tracing configuration from the standard OpenTelemetry
// environment variables:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT  OTLP/HTTP collector target. Empty (the
//	                             default) disables exporting.
//	OTEL_EXPORTER_OTLP_INSECURE  Boolean ("true", "1", ...) for plain HTTP;
//	                             defaults to false (HTTPS).
func ConfigFromEnv() Config {
	return Config{
		Endpoint: strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		Insecure: envBool("OTEL_EXPORTER_OTLP_INSECURE"),
	}
}

// envBool parses a boolean environment variable. Unset, empty and unparsable
// values all mean false so a typo cannot turn span export into a startup error.
func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

// NewProvider creates an SDK tracer provider. A caller may add a span exporter
// through sdktrace.WithBatcher when an OTLP collector is available; Setup does
// that wiring, so most code does not need this constructor directly.
func NewProvider(options ...sdktrace.TracerProviderOption) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(options...)
}

// newProvider builds the tracer provider used by Setup. It always installs the
// service resource and, when cfg.Endpoint is set, attaches a non-blocking
// batched OTLP/HTTP exporter. extra options are applied last, which lets tests
// attach an in-memory span recorder instead of a live collector.
func newProvider(ctx context.Context, cfg Config, extra ...sdktrace.TracerProviderOption) (*sdktrace.TracerProvider, error) {
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		)),
	}
	if cfg.Endpoint != "" {
		exporterOptions, err := exporterOptions(cfg)
		if err != nil {
			return nil, fmt.Errorf("observability: invalid OTLP endpoint %q: %w", cfg.Endpoint, err)
		}
		exporter, err := otlptracehttp.New(ctx, exporterOptions...)
		if err != nil {
			return nil, fmt.Errorf("observability: create OTLP/HTTP exporter: %w", err)
		}
		options = append(options, sdktrace.WithBatcher(exporter))
	}
	options = append(options, extra...)
	return NewProvider(options...), nil
}

// exporterOptions validates cfg.Endpoint and converts it into OTLP/HTTP
// exporter options. Validation happens here (before a collector is contacted)
// so a mistyped endpoint surfaces as a Setup error instead of silent drops.
func exporterOptions(cfg Config) ([]otlptracehttp.Option, error) {
	options := []otlptracehttp.Option{otlptracehttp.WithTimeout(exporterTimeout)}
	if strings.Contains(cfg.Endpoint, "://") {
		parsed, err := url.Parse(cfg.Endpoint)
		if err != nil {
			return nil, err
		}
		switch parsed.Scheme {
		case "http", "https":
		default:
			return nil, fmt.Errorf("unsupported scheme %q (want http or https)", parsed.Scheme)
		}
		if parsed.Host == "" {
			return nil, errors.New("URL has no host")
		}
		options = append(options, otlptracehttp.WithEndpointURL(cfg.Endpoint))
		if cfg.Insecure {
			options = append(options, otlptracehttp.WithInsecure())
		}
		return options, nil
	}
	if strings.ContainsAny(cfg.Endpoint, " \t\r\n") {
		return nil, errors.New("endpoint must not contain whitespace")
	}
	if strings.Contains(cfg.Endpoint, "/") {
		return nil, errors.New("use host:port or an http(s):// URL with a path")
	}
	options = append(options, otlptracehttp.WithEndpoint(cfg.Endpoint))
	if cfg.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}
	return options, nil
}
