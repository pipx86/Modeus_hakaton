package steps

import (
	"context"
	"sync"
	"testing"

	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/llmobs"
)

// runIDAnalystClient records the run id found in the call context.
type runIDAnalystClient struct {
	mu     sync.Mutex
	runIDs []string
}

func (c *runIDAnalystClient) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	c.mu.Lock()
	c.runIDs = append(c.runIDs, llmobs.RunIDFromContext(ctx))
	c.mu.Unlock()
	return validAnalystJSON, nil
}

func (c *runIDAnalystClient) capturedRunIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.runIDs...)
}

// TestAgentAnalysisStepAttachesRunID verifies the batch analyst call carries the
// run id in its context, so llmobs.Recorder stores llm_requests.run_id.
func TestAgentAnalysisStepAttachesRunID(t *testing.T) {
	client := &runIDAnalystClient{}
	step := &AgentAnalysisStep{Client: client}
	run := &domain.Run{
		ID:         "run-batch-context",
		Profession: "Аналитик данных",
	}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")
	if err := step.ExecuteWithData(context.Background(), run, nil); err != nil {
		t.Fatalf("ExecuteWithData: %v", err)
	}
	runIDs := client.capturedRunIDs()
	if len(runIDs) != 1 || runIDs[0] != "run-batch-context" {
		t.Fatalf("analyst run ids = %v, want [run-batch-context]", runIDs)
	}
}
