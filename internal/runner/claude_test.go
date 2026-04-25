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

func TestComputeDelta(t *testing.T) {
	cases := []struct {
		name    string
		already string
		next    string
		want    string
	}{
		{"empty next", "abc", "", ""},
		{"cumulative growth", "Hello", "Hello world", " world"},
		{"identical", "Hello", "Hello", ""},
		{"non-prefix treated as full chunk", "Hello", " world", " world"},
		{"first chunk", "", "Hi", "Hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeDelta(tc.already, tc.next); got != tc.want {
				t.Errorf("computeDelta(%q, %q) = %q, want %q", tc.already, tc.next, got, tc.want)
			}
		})
	}
}

func TestParseClaudeStreamEvent_AssistantText(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}]}}`)
	evt, err := parseClaudeStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if evt.Type != "assistant" {
		t.Errorf("type: want %q, got %q", "assistant", evt.Type)
	}
	if got := assistantText(evt.Message.Content); got != "Hello world" {
		t.Errorf("assistantText: want %q, got %q", "Hello world", got)
	}
}

func TestParseClaudeStreamEvent_Result(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"final","usage":{"input_tokens":10,"output_tokens":3}}`)
	evt, err := parseClaudeStreamEvent(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if evt.Result != "final" {
		t.Errorf("result: want %q, got %q", "final", evt.Result)
	}
	if evt.Usage.InputTokens != 10 || evt.Usage.OutputTokens != 3 {
		t.Errorf("usage: want 10/3, got %d/%d", evt.Usage.InputTokens, evt.Usage.OutputTokens)
	}
}
