package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestUnwrapJSONResponse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text untouched", "hello world", "hello world"},
		{"plain envelope", `{"response": "hi there"}`, "hi there"},
		{"envelope with reply key", `{"reply":"yo"}`, "yo"},
		{"json fence wrapper", "```json\n{\"response\": \"Zdefiniuję zadanie.\"}\n```", "Zdefiniuję zadanie."},
		{"unknown json kept as-is", `{"foo":"bar"}`, `{"foo":"bar"}`},
		{"malformed json kept as-is", `{not json`, `{not json`},
		{"empty stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unwrapJSONResponse(tc.in); got != tc.want {
				t.Errorf("unwrapJSONResponse(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestOllamaStreamResponse_UnwrapsJSONEnvelope(t *testing.T) {
	chunks := []ollamaChatResponse{
		{Message: ollamaChatMessage{Role: "assistant", Content: "```json\n{\"response\": \"czesc\"}"}},
		{Message: ollamaChatMessage{Role: "assistant", Content: "\n```"}, Done: true},
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

	var text string
	for tok := range ch {
		text += tok.Text
	}
	if text != "czesc" {
		t.Errorf("expected unwrapped text %q, got %q", "czesc", text)
	}
}

func TestOllamaRunner_SendsUnlimitedNumPredict(t *testing.T) {
	var captured ollamaChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	r := &OllamaRunner{Model: "test", BaseURL: srv.URL}
	ch, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []ConvMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for range ch {
	}

	if captured.Options.NumPredict != ollamaMaxPredict {
		t.Errorf("expected num_predict=%d, got %d", ollamaMaxPredict, captured.Options.NumPredict)
	}
}
