package main

import (
	"context"
	"errors"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/llmobs"
)

// runIDCaptureAnalyst captures the run id of the finalizer context and stops
// the pipeline by returning an error, so no other collaborator is needed.
type runIDCaptureAnalyst struct {
	runID string
}

func (a *runIDCaptureAnalyst) Analyze(ctx context.Context, _ agent.AnalystInput) (agent.AnalystOutput, error) {
	a.runID = llmobs.RunIDFromContext(ctx)
	return agent.AnalystOutput{}, errors.New("stop after context check")
}

// TestFinalizeInterviewAttachesRunID verifies the chat finalizer gives the run
// id to the analyst (and therefore to the recorded llm_requests rows).
func TestFinalizeInterviewAttachesRunID(t *testing.T) {
	analyst := &runIDCaptureAnalyst{}
	deps := finalizerDeps{
		store:   &fakeFinalizeStore{},
		analyst: analyst,
	}
	if _, err := finalizeInterview(context.Background(), deps, "run-chat-context", "Аналитик данных", testFinalizeSession()); err == nil {
		t.Fatal("expected the capturing analyst to stop finalization")
	}
	if analyst.runID != "run-chat-context" {
		t.Fatalf("analyst run id = %q, want run-chat-context", analyst.runID)
	}
}
