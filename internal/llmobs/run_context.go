package llmobs

import (
	"context"
	"strings"
)

// runIDKey is the context key type of the run id attached to one LLM call.
type runIDKey struct{}

// WithRunID attaches the research run id to ctx. The Recorder reads it when it
// writes llm_requests, so every request made for a run is attributed to that
// run instead of relying on a time-window guess.
//
// An empty (or whitespace-only) run id is a no-op and returns ctx unchanged:
// agents are also used outside a run (smoke commands, tests), and those calls
// must keep run_id NULL.
func WithRunID(ctx context.Context, runID string) context.Context {
	runID = strings.TrimSpace(runID)
	if ctx == nil || runID == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunIDFromContext returns the run id attached by WithRunID, or "" when the
// call does not belong to a research run.
func RunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(runIDKey{}).(string)
	return runID
}
