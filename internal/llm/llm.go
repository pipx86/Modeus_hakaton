// Package llm defines the contract between agents and an LLM provider.
package llm

import "context"

// Role is the author of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single chat message.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatClient is the minimal interface agents depend on.
type ChatClient interface {
	Chat(ctx context.Context, messages []Message) (string, error)
}

// Usage is token and model usage reported by a provider for one successful call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Model            string
}

// UsageObserver receives usage for every successful provider call. It must be
// safe for concurrent use.
type UsageObserver func(Usage)
