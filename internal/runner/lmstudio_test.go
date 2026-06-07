package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLMStudioStreamResponse_CapturesUsage(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello "}}]}`,
		`data: {"choices":[{"delta":{"content":"world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	r := NewLMStudioRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes([]byte(stream), "", ch)

	var text string
	var done Token
	for tok := range ch {
		text += tok.Text
		if tok.Done {
			done = tok
		}
	}

	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if done.Usage == nil {
		t.Fatal("expected Usage on Done token, got nil")
	}
	if done.Usage.InputTokens != 12 || done.Usage.OutputTokens != 5 {
		t.Errorf("usage: want 12/5, got %d/%d", done.Usage.InputTokens, done.Usage.OutputTokens)
	}
	if done.Usage.Estimated {
		t.Error("Estimated should be false for LM Studio API data")
	}
}

func TestLMStudioStreamResponse_EstimatesUsageWhenMissing(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
	r := NewLMStudioRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes([]byte(stream), "some input text", ch)

	var done Token
	for tok := range ch {
		if tok.Done {
			done = tok
		}
	}
	if done.Usage == nil || !done.Usage.Estimated {
		t.Fatalf("expected estimated usage when server omits it, got %+v", done.Usage)
	}
}

func TestLMStudioStreamResponse_EmptyStreamIsError(t *testing.T) {
	// Empty SSE stream (LM Studio's silent context-overflow failure mode):
	// the runner must surface an error rather than report empty success.
	stream := "data: [DONE]\n"
	r := NewLMStudioRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes([]byte(stream), "some input text", ch)

	var sawErr bool
	for tok := range ch {
		if tok.Error != nil {
			sawErr = true
		}
		if tok.Text != "" {
			t.Errorf("did not expect text on empty stream, got %q", tok.Text)
		}
	}
	if !sawErr {
		t.Error("expected an error for an empty LM Studio stream")
	}
}

func TestLMStudioStreamResponse_ReasoningOnlyIsError(t *testing.T) {
	// Model streams only reasoning_content and no answer.
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"thinking..."}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"more"},"finish_reason":"length"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	r := NewLMStudioRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes([]byte(stream), "in", ch)

	var errMsg string
	for tok := range ch {
		if tok.Error != nil {
			errMsg = tok.Error.Error()
		}
		if tok.Text != "" {
			t.Errorf("reasoning-only response must not yield text, got %q", tok.Text)
		}
	}
	if !strings.Contains(errMsg, "reasoning") {
		t.Errorf("expected a reasoning-related error, got %q", errMsg)
	}
}

func TestLMStudioRunner_SendsExpectedRequest(t *testing.T) {
	var captured lmStudioChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"))
	}))
	defer srv.Close()

	r := &LMStudioRunner{Model: "test-model", BaseURL: srv.URL}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		SystemPrompt: "be brief",
		Messages:     []ConvMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var text string
	for tok := range ch {
		text += tok.Text
	}
	if text != "ok" {
		t.Errorf("text: want %q, got %q", "ok", text)
	}
	if captured.Model != "test-model" {
		t.Errorf("model: want test-model, got %q", captured.Model)
	}
	if !captured.Stream {
		t.Error("expected stream=true")
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" || captured.Messages[1].Content != "hi" {
		t.Errorf("unexpected messages: %+v", captured.Messages)
	}
}
