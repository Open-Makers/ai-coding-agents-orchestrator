package runner

import (
	"context"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// BudgetRunner wraps an LLMRunner and truncates message content
// to stay within a token budget. Used for cloud runners where tokens have cost.
type BudgetRunner struct {
	inner     LLMRunner
	maxTokens int
}

func (r *BudgetRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	// Reserve ~20% of budget for system prompt + skills, rest for messages.
	systemBudget := r.maxTokens / 5
	messageBudget := r.maxTokens - systemBudget

	req.SystemPrompt = tokenutil.Truncate(req.SystemPrompt, systemBudget)

	for i := range req.Messages {
		msgTokens := tokenutil.EstimateTokens(req.Messages[i].Content)
		if msgTokens > messageBudget {
			req.Messages[i].Content = tokenutil.Truncate(req.Messages[i].Content, messageBudget)
		}
		messageBudget -= tokenutil.EstimateTokens(req.Messages[i].Content)
		if messageBudget <= 0 {
			break
		}
	}

	return r.inner.Complete(ctx, req)
}
