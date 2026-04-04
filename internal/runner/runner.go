package runner

import "context"

// ConvMessage is a single turn in a conversation.
type ConvMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

// Token is a single streaming chunk from an LLM.
type Token struct {
	Text  string
	Done  bool
	Error error
}

// CompletionRequest describes what to send to the LLM.
type CompletionRequest struct {
	SystemPrompt string
	Skills       []string
	Messages     []ConvMessage
	Model        string
}

// LLMRunner is the common interface for all LLM backends.
type LLMRunner interface {
	Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error)
}
