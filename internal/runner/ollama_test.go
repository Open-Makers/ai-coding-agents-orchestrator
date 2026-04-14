package runner

import (
	"encoding/json"
	"testing"
)

func TestOllamaStreamResponse_CapturesUsage(t *testing.T) {
	chunks := []ollamaChatResponse{
		{Message: ollamaChatMessage{Role: "assistant", Content: "hello "}},
		{Message: ollamaChatMessage{Role: "assistant", Content: "world"}, Done: true, PromptEvalCount: 42, EvalCount: 17},
	}

	var lines []byte
	for _, c := range chunks {
		b, _ := json.Marshal(c)
		lines = append(lines, b...)
		lines = append(lines, '\n')
	}

	r := NewOllamaRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes(lines, ch)

	var tokens []Token
	for tok := range ch {
		tokens = append(tokens, tok)
	}

	done := tokens[len(tokens)-1]
	if !done.Done {
		t.Fatal("last token should be Done")
	}
	if done.Usage == nil {
		t.Fatal("expected Usage on Done token, got nil")
	}
	if done.Usage.InputTokens != 42 {
		t.Errorf("InputTokens: want 42, got %d", done.Usage.InputTokens)
	}
	if done.Usage.OutputTokens != 17 {
		t.Errorf("OutputTokens: want 17, got %d", done.Usage.OutputTokens)
	}
	if done.Usage.Estimated {
		t.Error("Estimated should be false for Ollama API data")
	}
}
