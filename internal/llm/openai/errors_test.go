package openai_test

import (
	"testing"

	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llm/openai"
)

func TestProviderUpstreamClassification(t *testing.T) {
	tests := []struct {
		status int
		kind   llm.ErrorKind
		retry  bool
	}{
		{401, llm.ErrorAuth, false},
		{403, llm.ErrorAuth, false},
		{429, llm.ErrorRateLimit, true},
		{500, llm.ErrorUnavailable, true},
		{503, llm.ErrorUnavailable, true},
		{422, llm.ErrorInvalidResponse, false},
	}
	for _, tt := range tests {
		upstream := &openai.UpstreamError{StatusCode: tt.status}
		if got := llm.Classify(upstream); got != tt.kind {
			t.Fatalf("status %d: Classify = %q, want %q", tt.status, got, tt.kind)
		}
		if got := llm.IsRetryable(upstream); got != tt.retry {
			t.Fatalf("status %d: IsRetryable = %v, want %v", tt.status, got, tt.retry)
		}
	}
}
