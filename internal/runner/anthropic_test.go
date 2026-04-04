package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sseBody(events []string) string {
	var sb string
	for _, e := range events {
		sb += "data: " + e + "\n\n"
	}
	return sb
}

func TestAnthropicRunner_Stream(t *testing.T) {
	events := []string{
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":", world"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"!"}}`,
		`{"type":"message_stop"}`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody(events))
	}))
	defer srv.Close()

	r := &AnthropicRunner{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
		Model:    "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var texts []string
	var gotDone bool
	for tok := range ch {
		if tok.Error != nil {
			t.Fatalf("token error: %v", tok.Error)
		}
		if tok.Done {
			gotDone = true
		} else {
			texts = append(texts, tok.Text)
		}
	}

	if !gotDone {
		t.Error("expected Done token")
	}
	full := ""
	for _, t := range texts {
		full += t
	}
	if full != "Hello, world!" {
		t.Errorf("unexpected output: %q", full)
	}
}

func TestAnthropicRunner_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid api key"}`)
	}))
	defer srv.Close()

	r := &AnthropicRunner{
		apiKey:     "bad-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestAnthropicRunner_ContextCancel(t *testing.T) {
	// Verify that a pre-cancelled context causes Complete to return an error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := &AnthropicRunner{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := r.Complete(ctx, CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Error("expected error for pre-cancelled context")
	}
}

// Compile-time check: AnthropicRunner implements LLMRunner.
var _ LLMRunner = &AnthropicRunner{}
