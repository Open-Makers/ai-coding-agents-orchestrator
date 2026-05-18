package runner

import (
	"context"
	"testing"
)

func TestMockRunner_Basic(t *testing.T) {
	m := &MockRunner{Responses: []string{"hello world"}}

	ch, err := m.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var tokens []Token
	for tok := range ch {
		tokens = append(tokens, tok)
	}

	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens (text + done), got %d", len(tokens))
	}
	if tokens[0].Text != "hello world" {
		t.Errorf("unexpected text: %q", tokens[0].Text)
	}
	if !tokens[1].Done {
		t.Error("expected final token to be Done")
	}
}

func TestMockRunner_Exhausted(t *testing.T) {
	m := &MockRunner{Responses: []string{"only one"}}
	_, _ = m.Complete(context.Background(), CompletionRequest{})

	_, err := m.Complete(context.Background(), CompletionRequest{})
	if err == nil {
		t.Error("expected error when responses exhausted")
	}
}

func TestMockRunner_Reset(t *testing.T) {
	m := &MockRunner{Responses: []string{"a"}}
	_, _ = m.Complete(context.Background(), CompletionRequest{})
	m.Reset()

	ch, err := m.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
	for range ch {
	}
}

// Compile-time check: ClaudeRunner implements LLMRunner.
var _ LLMRunner = ClaudeRunner{}

// Compile-time check: OpenCodeRunner implements LLMRunner.
var _ LLMRunner = OpenCodeRunner{}

// Compile-time check: OllamaRunner implements LLMRunner.
var _ LLMRunner = &OllamaRunner{}

// Compile-time check: MLXRunner implements LLMRunner.
var _ LLMRunner = &MLXRunner{}

// Compile-time check: CodexRunner implements LLMRunner.
var _ LLMRunner = CodexRunner{}

// Compile-time check: SkillRunner implements LLMRunner.
var _ LLMRunner = &SkillRunner{}

// Compile-time check: BudgetRunner implements LLMRunner.
var _ LLMRunner = &BudgetRunner{}

// Compile-time check: MockRunner implements LLMRunner.
var _ LLMRunner = &MockRunner{}

func TestMockRunner_EmitsUsageOnDone(t *testing.T) {
	m := &MockRunner{
		Responses: []string{"hello"},
		MockUsage: &TokenUsage{InputTokens: 10, OutputTokens: 5},
	}

	ch, err := m.Complete(context.Background(), CompletionRequest{})
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
		t.Fatal("expected Usage on Done token, got nil")
	}
	if done.Usage.InputTokens != 10 || done.Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", done.Usage)
	}
}
