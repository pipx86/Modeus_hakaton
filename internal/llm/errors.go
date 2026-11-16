package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorKind is a stable, provider-independent error category.
type ErrorKind string

const (
	ErrorAuth            ErrorKind = "auth"
	ErrorRateLimit       ErrorKind = "rate_limit"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorInvalidResponse ErrorKind = "invalid_response"
	ErrorGuardrail       ErrorKind = "guardrail"
	ErrorCanceled        ErrorKind = "canceled"
	ErrorConfiguration   ErrorKind = "configuration"
	ErrorInternal        ErrorKind = "internal"
)

// Classified is implemented by provider errors that know their category,
// retryability and HTTP status.
type Classified interface {
	Kind() ErrorKind
	Retryable() bool
	HTTPStatus() int
}

// DomainError is the unified LLM error. It never contains prompt text, PII or
// secrets: only category, provider, status, retryability and the underlying
// cause (which itself must stay safe to log).
type DomainError struct {
	Category ErrorKind
	Provider string
	Status   int
	Retry    bool
	Cause    error
}

// NewDomainError builds a classified LLM error.
func NewDomainError(kind ErrorKind, provider string, status int, retry bool, cause error) *DomainError {
	return &DomainError{Category: kind, Provider: provider, Status: status, Retry: retry, Cause: cause}
}

// Error implements error. It deliberately does not print raw provider bodies.
func (e *DomainError) Error() string {
	if e == nil {
		return "llm: error"
	}
	parts := []string{"llm", string(e.Category)}
	if e.Provider != "" {
		parts = append(parts, e.Provider)
	}
	if e.Status != 0 {
		parts = append(parts, fmt.Sprintf("status %d", e.Status))
	}
	return strings.Join(parts, ": ")
}

// Unwrap allows errors.Is/errors.As to reach the original error.
func (e *DomainError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Kind implements Classified.
func (e *DomainError) Kind() ErrorKind {
	if e == nil {
		return ErrorInternal
	}
	return e.Category
}

// Retryable implements Classified.
func (e *DomainError) Retryable() bool { return e != nil && e.Retry }

// HTTPStatus implements Classified.
func (e *DomainError) HTTPStatus() int {
	if e == nil {
		return 0
	}
	return e.Status
}

// KindForStatus maps an HTTP status to a stable category.
func KindForStatus(status int) ErrorKind {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrorAuth
	case status == http.StatusTooManyRequests:
		return ErrorRateLimit
	case status == http.StatusRequestTimeout:
		return ErrorTimeout
	case status >= http.StatusInternalServerError:
		return ErrorUnavailable
	case status >= http.StatusBadRequest:
		return ErrorInvalidResponse
	default:
		return ErrorInternal
	}
}

// RetryableStatus reports whether a provider HTTP status is worth retrying.
func RetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

// Classify returns the category of err. It understands DomainError, provider
// errors implementing Classified, context cancellation and LLM guard errors.
func Classify(err error) ErrorKind {
	if err == nil {
		return ""
	}
	var classified Classified
	if errors.As(err, &classified) {
		return classified.Kind()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrorCanceled
	}
	var guard *GuardError
	if errors.As(err, &guard) {
		return ErrorGuardrail
	}
	if errors.Is(err, ErrProviderTimeout) {
		return ErrorTimeout
	}
	if errors.Is(err, ErrCircuitOpen) {
		return ErrorUnavailable
	}
	return ErrorInternal
}

// IsRetryable reports whether err may be retried without changing the request.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var classified Classified
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return false
}

// Code returns a short stable code for logs and API responses.
func Code(err error) string {
	kind := Classify(err)
	if kind == "" {
		return "ok"
	}
	return string(kind)
}

// SafeMessage returns a user-safe message that never includes provider bodies,
// prompts or PII.
func SafeMessage(err error) string {
	switch Classify(err) {
	case ErrorAuth:
		return "provider authentication failed"
	case ErrorRateLimit:
		return "provider rate limit exceeded"
	case ErrorTimeout:
		return "provider request timed out"
	case ErrorUnavailable:
		return "provider is temporarily unavailable"
	case ErrorInvalidResponse:
		return "provider returned an invalid response"
	case ErrorGuardrail:
		return "request blocked by guardrails"
	case ErrorCanceled:
		return "request was canceled"
	case ErrorConfiguration:
		return "LLM provider is not configured"
	case ErrorInternal:
		return "internal LLM error"
	default:
		return "ok"
	}
}
