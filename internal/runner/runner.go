package runner

import (
	"context"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
)

// TokenUsage holds token counts for a single LLM completion.
// Estimated is true when the count was derived from a heuristic (chars÷4)
// rather than actual API data.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	Estimated    bool
}

// ConvMessage is a single turn in a conversation.
type ConvMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

// Token is a single streaming chunk from an LLM.
type Token struct {
	Text string
	// Reasoning is display-only chain-of-thought from reasoning models. It is
	// shown live in the UI but NOT included in the returned/parsed result.
	Reasoning string
	Done      bool
	Error     error
	Usage     *TokenUsage // non-nil only on the Done token
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

// ResolveSkills loads skill content and appends it to the system prompt.
// Returns the enriched system prompt. If loader is nil or no skills match,
// the original prompt is returned unchanged.
func ResolveSkills(systemPrompt string, skillNames []string, loader *skills.Loader) string {
	if loader == nil || len(skillNames) == 0 {
		return systemPrompt
	}

	var loaded []string
	for _, name := range skillNames {
		content, err := loader.Load(name)
		if err != nil || content == "" {
			continue
		}
		loaded = append(loaded, content)
	}

	if len(loaded) == 0 {
		return systemPrompt
	}

	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n<skills>\n")
	sb.WriteString(strings.Join(loaded, "\n---\n"))
	sb.WriteString("\n</skills>")
	return sb.String()
}
