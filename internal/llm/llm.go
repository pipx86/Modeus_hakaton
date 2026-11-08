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
