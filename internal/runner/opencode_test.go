package runner

import (
	"testing"
)

func TestExtractOpenCodeResponse_WithUsageEvent(t *testing.T) {
	ndjson := []byte(`{"type":"text","part":{"text":"hello "}}
{"type":"text","part":{"text":"world"}}
{"type":"usage","usage":{"input_tokens":80,"output_tokens":12}}
`)

	text, usage := extractOpenCodeResponseWithUsage(ndjson, "input prompt")
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if usage.InputTokens != 80 {
		t.Errorf("InputTokens: want 80, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 12 {
		t.Errorf("OutputTokens: want 12, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false when usage event found")
	}
}

func TestExtractOpenCodeResponse_FallbackEstimate(t *testing.T) {
	ndjson := []byte(`{"type":"text","part":{"text":"hello world"}}
`)

	text, usage := extractOpenCodeResponseWithUsage(ndjson, "some input prompt for estimation")
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true when no usage event in stream")
	}
	if usage.OutputTokens <= 0 {
		t.Error("expected positive OutputTokens on fallback estimate")
	}
	if usage.InputTokens <= 0 {
		t.Error("expected positive InputTokens on fallback estimate (local-model monitoring relies on this)")
	}
}
