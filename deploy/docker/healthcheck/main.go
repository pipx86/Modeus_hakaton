// Command healthcheck is the container-level liveness probe for the distroless
// cmd/server image (the "server" Dockerfile target). Distroless images have no
// shell or package manager, so neither a shell HEALTHCHECK nor curl/wget is
// available: this small static binary performs the HTTP request instead.
//
// The URL defaults to http://<HTTP_ADDR>/api/v1/health, matching the listen
// address that cmd/server reads from HTTP_ADDR; an explicit -url overrides it.
// Exit code 0 means the backend answered with a 2xx status.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	url := flag.String("url", "", "health URL (default: http://<HTTP_ADDR>/api/v1/health)")
	timeout := flag.Duration("timeout", 3*time.Second, "request timeout")
	flag.Parse()

	target := strings.TrimSpace(*url)
	if target == "" {
		target = probeURL(os.Getenv("HTTP_ADDR"))
	}

	if err := probe(target, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
}

func probe(target string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", target, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GET %s: status %d", target, res.StatusCode)
	}
	return nil
}

// probeURL derives the loopback health URL from the server listen address.
// cmd/server listens on HTTP_ADDR (host:port) and defaults to 127.0.0.1:8080;
// ":8080"/"0.0.0.0:8080" are rewritten to loopback so the probe never leaves
// the container.
func probeURL(addr string) string {
	host := strings.TrimSpace(addr)
	if host == "" {
		host = "127.0.0.1:8080"
	}
	host = strings.TrimPrefix(host, "tcp://")
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	if strings.HasPrefix(host, "0.0.0.0:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
	}
	if !strings.Contains(host, ":") {
		host = "127.0.0.1:" + host
	}
	return "http://" + host + "/api/v1/health"
}
