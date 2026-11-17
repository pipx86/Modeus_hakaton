package agents

import (
	"context"
	"fmt"

	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/observability"

	"go.opentelemetry.io/otel/attribute"
)

// TracedClient wraps one agent client with an OpenTelemetry span.
type TracedClient struct {
	Next      llm.ChatClient
	Agent     AgentName
	Model     string
	Reasoning string
}

// NewTracedClient wraps next with an agent.chat span.
func NewTracedClient(next llm.ChatClient, agent AgentName, model, reasoning string) *TracedClient {
	return &TracedClient{Next: next, Agent: agent, Model: model, Reasoning: reasoning}
}

// Chat records one agent call and ends the span with the provider result.
func (t *TracedClient) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	if t == nil || t.Next == nil {
		return "", fmt.Errorf("agents: traced client has no next client")
	}
	ctx, span := observability.Start(ctx, "agent.chat",
		attribute.String("agent", string(t.Agent)),
		attribute.String("model", t.Model),
		attribute.String("reasoning", t.Reasoning),
	)
	answer, err := t.Next.Chat(ctx, messages)
	observability.Finish(span, err)
	return answer, err
}
