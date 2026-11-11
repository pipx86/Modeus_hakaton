package ollama

import (
	"errors"
	"fmt"
)

var (
	// ErrUpstream marks non-2xx responses from Ollama.
	ErrUpstream = errors.New("ollama: upstream error")
	// ErrTimeout marks request timeouts and deadline exceeded.
	ErrTimeout = errors.New("ollama: timeout")
	// ErrDecode marks malformed or empty responses.
	ErrDecode = errors.New("ollama: decode error")
)

// UpstreamError describes a non-2xx response from Ollama.
type UpstreamError struct {
	StatusCode int
	Body       string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("ollama: upstream returned status %d", e.StatusCode)
}

// Unwrap allows errors.Is(err, ErrUpstream).
func (e *UpstreamError) Unwrap() error { return ErrUpstream }
