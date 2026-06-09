package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMLXStreamResponse_CapturesUsage(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello "}}]}`,
		`data: {"choices":[{"delta":{"content":"world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"completion_tokens":17}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	r := NewMLXRunner("")
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
	if done.Usage.InputTokens != 42 || done.Usage.OutputTokens != 17 {
		t.Errorf("usage: want 42/17, got %d/%d", done.Usage.InputTokens, done.Usage.OutputTokens)
	}
	if done.Usage.Estimated {
		t.Error("Estimated should be false for oMLX API data")
	}
}

func TestMLXRunner_SendsExpectedRequest(t *testing.T) {
	var captured mlxChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"))
	}))
	defer srv.Close()

	r := &MLXRunner{Model: "test-model", BaseURL: srv.URL}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for range ch {
	}

	if captured.Model != "test-model" {
		t.Errorf("model: want %q, got %q", "test-model", captured.Model)
	}
	if !captured.Stream {
		t.Error("expected stream=true")
	}
	if captured.MaxTokens != mlxMaxTokens {
		t.Errorf("max_tokens: want %d, got %d", mlxMaxTokens, captured.MaxTokens)
	}
}

func TestMLXStreamResponse_EmptyIsError(t *testing.T) {
	// HTTP 200 with an empty content stream must surface a descriptive error,
	// not a silent success (which downstream agents misreport).
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{}}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	r := NewMLXRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes([]byte(stream), "some prompt", ch)

	var sawErr bool
	for tok := range ch {
		if tok.Error != nil {
			sawErr = true
			if !strings.Contains(tok.Error.Error(), "omlx") {
				t.Errorf("expected an omlx-tagged error, got %q", tok.Error.Error())
			}
		}
		if tok.Text != "" {
			t.Errorf("empty response must not yield text, got %q", tok.Text)
		}
	}
	if !sawErr {
		t.Error("expected an error for an empty oMLX stream")
	}
}

func TestMLXStreamResponse_ReasoningOnlyIsError(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"thinking…"}}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	r := NewMLXRunner("")
	ch := make(chan Token, 16)
	go r.streamResponseFromBytes([]byte(stream), "p", ch)

	var errMsg string
	for tok := range ch {
		if tok.Error != nil {
			errMsg = tok.Error.Error()
		}
	}
	if !strings.Contains(errMsg, "reasoning") {
		t.Errorf("expected a reasoning-related error, got %q", errMsg)
	}
}
