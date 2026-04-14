package runner

import (
	"testing"
)

func TestCountCodexTokens_Basic(t *testing.T) {
	usage := countCodexTokens("gpt-4o", "hello world system prompt", "hello world response")
	if usage.InputTokens <= 0 {
		t.Errorf("expected positive InputTokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected positive OutputTokens, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false when tiktoken succeeds")
	}
}

func TestCountCodexTokens_UnknownModel_FallsBackToEstimate(t *testing.T) {
	usage := countCodexTokens("unknown-model-xyz", "hello", "world")
	if usage.InputTokens <= 0 {
		t.Errorf("expected positive InputTokens even on fallback, got %d", usage.InputTokens)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true on tiktoken fallback")
	}
}
