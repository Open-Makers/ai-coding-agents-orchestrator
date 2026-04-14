package runner

import (
	"testing"
)

func TestClaudeParseJSONResponse(t *testing.T) {
	input := []byte(`{"type":"result","subtype":"success","result":"hello world","total_input_tokens":120,"total_output_tokens":45,"usage":{"input_tokens":120,"output_tokens":45,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)

	text, usage, err := parseClaudeJSONResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if usage.InputTokens != 120 {
		t.Errorf("InputTokens: want 120, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 45 {
		t.Errorf("OutputTokens: want 45, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false for Claude JSON response")
	}
}

func TestClaudeParseJSONResponse_FallbackOnMalformed(t *testing.T) {
	input := []byte("plain text response without json")

	text, usage, err := parseClaudeJSONResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "plain text response without json" {
		t.Errorf("unexpected text: %q", text)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true for fallback")
	}
}
