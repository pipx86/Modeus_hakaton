package openai

import (
	"errors"
	"fmt"

	"activity-descriptor/internal/llm"
)

var (
	// ErrUpstream marks non-2xx responses from the provider.
	ErrUpstream = errors.New("openai: upstream error")
	// ErrTimeout marks request timeouts and deadline exceeded.
	ErrTimeout = errors.New("openai: timeout")
	// ErrDecode marks malformed or empty responses.
	ErrDecode = errors.New("openai: decode error")
)

// UpstreamError describes a non-2xx response from the provider.
type UpstreamError struct {
	StatusCode int
	Body       string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("openai: upstream returned status %d", e.StatusCode)
}

// Unwrap allows errors.Is(err, ErrUpstream).
func (e *UpstreamError) Unwrap() error { return ErrUpstream }

// Kind implements llm.Classified.
func (e *UpstreamError) Kind() llm.ErrorKind { return llm.KindForStatus(e.StatusCode) }

// Retryable implements llm.Classified.
func (e *UpstreamError) Retryable() bool { return llm.RetryableStatus(e.StatusCode) }

// HTTPStatus implements llm.Classified.
func (e *UpstreamError) HTTPStatus() int { return e.StatusCode }
