package runner

import (
	"context"
	"testing"
)

func TestBudgetRunner_PassesThroughUsage(t *testing.T) {
	inner := &MockRunner{
		Responses: []string{"response text"},
		MockUsage: &TokenUsage{InputTokens: 100, OutputTokens: 50},
	}
	budget := &BudgetRunner{inner: inner, maxTokens: 10000}

	ch, err := budget.Complete(context.Background(), CompletionRequest{
		SystemPrompt: "system",
		Messages:     []ConvMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var done Token
	for tok := range ch {
		if tok.Done {
			done = tok
		}
	}

	if done.Usage == nil {
		t.Fatal("BudgetRunner should pass through Usage from inner runner")
	}
	if done.Usage.InputTokens != 100 || done.Usage.OutputTokens != 50 {
		t.Errorf("unexpected usage: %+v", done.Usage)
	}
}
