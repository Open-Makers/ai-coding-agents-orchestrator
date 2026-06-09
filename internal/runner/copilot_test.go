package runner

import (
	"bytes"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

func TestParseCopilotModelsOutput_TableFormat(t *testing.T) {
	in := []byte(`DISPLAY NAME       ID
GPT-5              openai/gpt-5
GPT-4.1            openai/gpt-4.1
Claude Sonnet 4.5  anthropic/claude-sonnet-4.5
`)
	got := parseCopilotModelsOutput(in)
	want := []string{"openai/gpt-5", "openai/gpt-4.1", "anthropic/claude-sonnet-4.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCopilotModelsOutput = %v, want %v", got, want)
	}
}

func TestParseCopilotModelsOutput_PlainList(t *testing.T) {
	in := []byte("gpt-5\ngpt-5-mini\n\nclaude-sonnet-4.5\n")
	got := parseCopilotModelsOutput(in)
	want := []string{"gpt-5", "gpt-5-mini", "claude-sonnet-4.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCopilotModelsOutput = %v, want %v", got, want)
	}
}

func TestParseCopilotModelsOutput_SkipsHeadersAndJunk(t *testing.T) {
	in := []byte("MODEL\n# comment\n   \nclaude opus 4.5\ngpt-5\n")
	got := parseCopilotModelsOutput(in)
	// "claude opus 4.5" → last token "4.5" doesn't look like a model id, skipped.
	want := []string{"gpt-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCopilotModelsOutput = %v, want %v", got, want)
	}
}

func TestLooksLikeCopilotModelID(t *testing.T) {
	cases := map[string]bool{
		"gpt-5":                       true,
		"openai/gpt-4.1":              true,
		"anthropic/claude-sonnet-4.5": true,
		"with space":                  false,
		"":                            false,
		"4.5":                         false, // bare version number, no letter
	}
	for in, want := range cases {
		if got := looksLikeCopilotModelID(in); got != want {
			t.Errorf("looksLikeCopilotModelID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCopilotFallbackModels_NoConfiguredReturnsDefaults(t *testing.T) {
	got := copilotFallbackModels("")
	if !reflect.DeepEqual(got, CopilotModels) {
		t.Fatalf("copilotFallbackModels(\"\") = %v, want %v", got, CopilotModels)
	}
}

func TestCopilotFallbackModels_ConfiguredMovedToFront(t *testing.T) {
	got := copilotFallbackModels("claude-opus-4.7")
	if len(got) != len(CopilotModels) {
		t.Fatalf("len = %d, want %d", len(got), len(CopilotModels))
	}
	if got[0] != "claude-opus-4.7" {
		t.Fatalf("got[0] = %q, want claude-opus-4.7", got[0])
	}
	for _, m := range got[1:] {
		if m == "claude-opus-4.7" {
			t.Fatalf("duplicate configured model in tail: %v", got)
		}
	}
}

func TestCopilotFallbackModels_ConfiguredNotInListIsPrepended(t *testing.T) {
	got := copilotFallbackModels("custom-byok-model")
	if got[0] != "custom-byok-model" {
		t.Fatalf("got[0] = %q, want custom-byok-model", got[0])
	}
	if len(got) != len(CopilotModels)+1 {
		t.Fatalf("len = %d, want %d", len(got), len(CopilotModels)+1)
	}
}

func TestStreamCopilotOutput_IncrementalUsage(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	const input = "system prompt and user content"
	const output = "hello world, here is some streamed answer text"
	body := io.NopCloser(strings.NewReader(output))

	ch := make(chan Token, 128)
	go streamCopilotOutput(cmd, body, &bytes.Buffer{}, input, ch)

	var inputTotal, outputTotal int
	var sawLiveOutput, done bool
	for tok := range ch {
		if tok.Usage != nil {
			inputTotal += tok.Usage.InputTokens
			outputTotal += tok.Usage.OutputTokens
			if tok.Usage.OutputTokens > 0 && !tok.Done {
				sawLiveOutput = true
			}
		}
		if tok.Done {
			done = true
		}
	}

	if !done {
		t.Fatal("stream never produced a Done token")
	}
	if !sawLiveOutput {
		t.Error("expected at least one intermediate (live) output-usage token")
	}
	if want := tokenutil.EstimateTokens(input); inputTotal != want {
		t.Errorf("input tokens: got %d, want %d", inputTotal, want)
	}
	if want := tokenutil.EstimateTokens(output); outputTotal != want {
		t.Errorf("output tokens (sum of deltas + reconciliation): got %d, want %d", outputTotal, want)
	}
}
