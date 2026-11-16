package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifyDomainErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    ErrorKind
		retry   bool
		message string
	}{
		{"auth", NewDomainError(ErrorAuth, "openai", 401, false, errors.New("bad key")), ErrorAuth, false, "authentication"},
		{"rate", NewDomainError(ErrorRateLimit, "openai", 429, true, errors.New("slow down")), ErrorRateLimit, true, "rate limit"},
		{"timeout", NewDomainError(ErrorTimeout, "openai", 0, true, context.DeadlineExceeded), ErrorTimeout, true, "timed out"},
		{"unavailable", NewDomainError(ErrorUnavailable, "openai", 503, true, errors.New("down")), ErrorUnavailable, true, "unavailable"},
		{"invalid", NewDomainError(ErrorInvalidResponse, "openai", 0, false, errors.New("bad json")), ErrorInvalidResponse, false, "invalid response"},
		{"guardrail", &GuardError{Kind: GuardKindJailbreak}, ErrorGuardrail, false, "guardrails"},
		{"canceled", NewDomainError(ErrorCanceled, "openai", 0, false, context.Canceled), ErrorCanceled, false, "canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Fatalf("Classify = %q, want %q", got, tt.want)
			}
			if got := IsRetryable(tt.err); got != tt.retry {
				t.Fatalf("IsRetryable = %v, want %v", got, tt.retry)
			}
			if got := Code(tt.err); got != string(tt.want) {
				t.Fatalf("Code = %q, want %q", got, tt.want)
			}
			if got := SafeMessage(tt.err); !strings.Contains(got, tt.message) {
				t.Fatalf("SafeMessage = %q, want containment %q", got, tt.message)
			}
		})
	}
}

func TestClassifyWrappedAndContext(t *testing.T) {
	wrapped := fmt.Errorf("agent: %w", NewDomainError(ErrorRateLimit, "openai", 429, true, errors.New("body-not-printed")))
	if Classify(wrapped) != ErrorRateLimit || !IsRetryable(wrapped) {
		t.Fatalf("wrapped classification failed: %v", wrapped)
	}
	if Classify(context.DeadlineExceeded) != ErrorTimeout || !IsRetryable(context.DeadlineExceeded) {
		t.Fatal("deadline classification failed")
	}
	if Classify(context.Canceled) != ErrorCanceled || IsRetryable(context.Canceled) {
		t.Fatal("cancellation classification failed")
	}
	if Classify(errors.New("plain")) != ErrorInternal {
		t.Fatal("plain error must be internal")
	}
}

func TestDomainErrorDoesNotExposeCause(t *testing.T) {
	secretCause := errors.New("provider body with secret")
	err := NewDomainError(ErrorUnavailable, "openai", 503, true, secretCause)
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("Error() leaks cause: %q", err.Error())
	}
	if !errors.Is(err, secretCause) {
		t.Fatal("Unwrap lost the cause")
	}
}
