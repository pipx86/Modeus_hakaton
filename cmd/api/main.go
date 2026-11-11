// Command api runs the demo activity-descriptor stack: the in-memory API store
// and the interviewer chat service with deterministic fallback questions, so
// the Docker/compose stack works without PostgreSQL migrations or an LLM
// provider. It also exposes the operational surfaces used by the DevOps stack:
//
//	GET /health          demo liveness/readiness probe
//	GET /metrics         Prometheus text exposition (stdlib)
//	GET /api/v1/health   backend health (public)
//	/api/v1/*            existing backend REST + chat endpoints (JWT protected)
//
// Production keeps cmd/server (PostgreSQL) for the backend owner; this command
// is the demo entrypoint for docker compose and local smoke tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"activity-descriptor/internal/api"
	"activity-descriptor/internal/chat"
	"activity-descriptor/internal/metrics"
)

// Version is the demo API version reported by /health.
const Version = "0.2.0-demo"

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe /health and exit (used by the container healthcheck)")
	addr := flag.String("addr", getenv("HTTP_ADDR", getenv("API_ADDR", ":8080")), "HTTP listen address")
	flag.Parse()

	if *healthcheck {
		if err := probe(probeURL(*addr)); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
			os.Exit(1)
		}
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(os.Getenv("LOG_LEVEL"))})).
		With("service", "activity-descriptor-api", "env", getenv("APP_ENV", "local"), "version", Version)

	if os.Getenv("JWT_SECRET") == "" {
		logger.Warn("JWT_SECRET is not set; protected /api/v1 endpoints will return 401")
	}

	m := metrics.New()
	server := &http.Server{
		Addr:              *addr,
		Handler:           newDemoHandler(m, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// No WriteTimeout: the chat SSE stream (/api/v1/runs/{id}/events) is long-lived.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		logger.Info("demo api listening", "addr", *addr, "metrics_path", "/metrics")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("demo api listen", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	timeout := 10 * time.Second
	if raw := os.Getenv("SHUTDOWN_TIMEOUT_S"); raw != "" {
		if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
			timeout = seconds
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("demo api shutdown", "error", err)
		os.Exit(1)
	}
	logger.Info("demo api stopped")
}

// newDemoHandler wires the demo surfaces. /health and /metrics are public;
// the backend router keeps its own /api/v1/health and JWT-protected REST/chat.
func newDemoHandler(m *metrics.Metrics, logger *slog.Logger) http.Handler {
	store := api.NewMemoryStore()
	chatService := chat.NewService(chat.UnavailableClient{}, nil)
	backend := api.NewServerWithChat(store, chatService).Routes()

	mux := http.NewServeMux()
	mux.Handle("GET /health", healthHandler())
	mux.Handle("GET /metrics", m.Handler())
	mux.Handle("/", backend)

	return requestLogger(logger, m.Middleware(mux))
}

type healthResponse struct {
	Status  string    `json:"status"`
	Service string    `json:"service"`
	Version string    `json:"version"`
	Mode    string    `json:"mode"`
	Store   string    `json:"store"`
	Chat    string    `json:"chat"`
	Time    time.Time `json:"time"`
}

func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{
			Status:  "ok",
			Service: "activity-descriptor-api",
			Version: Version,
			Mode:    "demo",
			Store:   "in-memory",
			Chat:    "fallback (no LLM; deterministic questions)",
			Time:    time.Now().UTC(),
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

// requestLogger logs one JSON record per request. It never logs bodies,
// headers or query strings, so prompts and secrets stay out of the logs.
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"latency_ms", time.Since(start).Milliseconds(),
			"bytes", rec.bytes,
			"request_id", r.Header.Get("X-Request-ID"),
		)
	})
}

// probe performs the HTTP healthcheck used by the container HEALTHCHECK.
func probe(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(url) //nolint:noctx // short-lived probe process
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}

// probeURL derives the local health URL from the listen address.
func probeURL(addr string) string {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	if strings.HasPrefix(host, "0.0.0.0:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
	}
	if !strings.Contains(host, ":") {
		host = "127.0.0.1:" + host
	}
	return "http://" + host + "/health"
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func logLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
