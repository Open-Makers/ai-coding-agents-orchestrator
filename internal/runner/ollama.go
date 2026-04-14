package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	ollamaDefaultModel = "qwen2.5-coder:latest"
	ollamaBaseURL      = "http://localhost:11434"
)

// OllamaRunner calls the Ollama REST API directly (/api/chat)
// instead of shelling out through the Claude Code CLI.
type OllamaRunner struct {
	Model   string
	BaseURL string
}

func NewOllamaRunner(model string) *OllamaRunner {
	if model == "" {
		model = ollamaDefaultModel
	}
	return &OllamaRunner{Model: model, BaseURL: ollamaBaseURL}
}

func (r *OllamaRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = r.Model
	}

	messages := r.buildMessages(req)
	body := ollamaChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	endpoint := r.baseURL() + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: not reachable at %s (is ollama running?): %w", r.baseURL(), err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama: model %q returned HTTP %d: %s", model, resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	ch := make(chan Token, 16)
	go r.streamResponse(resp.Body, ch)
	return ch, nil
}

func (r *OllamaRunner) buildMessages(req CompletionRequest) []ollamaChatMessage {
	var msgs []ollamaChatMessage

	if req.SystemPrompt != "" {
		msgs = append(msgs, ollamaChatMessage{
			Role:    "system",
			Content: req.SystemPrompt + "\n\nIMPORTANT: Output plain text only, not JSON.",
		})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, ollamaChatMessage(m))
	}

	return msgs
}

func (r *OllamaRunner) streamResponse(body io.ReadCloser, ch chan<- Token) {
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		ch <- Token{Error: fmt.Errorf("ollama: read stream: %w", err)}
		ch <- Token{Done: true}
		close(ch)
		return
	}
	r.streamResponseFromBytes(data, ch)
}

func (r *OllamaRunner) streamResponseFromBytes(data []byte, ch chan<- Token) {
	defer close(ch)
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var chunk ollamaChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			ch <- Token{Error: fmt.Errorf("ollama: decode stream chunk: %w", err)}
			ch <- Token{Done: true}
			return
		}
		if chunk.Message.Content != "" {
			ch <- Token{Text: chunk.Message.Content}
		}
		if chunk.Done {
			ch <- Token{
				Done: true,
				Usage: &TokenUsage{
					InputTokens:  chunk.PromptEvalCount,
					OutputTokens: chunk.EvalCount,
					Estimated:    false,
				},
			}
			return
		}
	}
	ch <- Token{Done: true}
}

func (r *OllamaRunner) baseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return ollamaBaseURL
}

// ollamaChatRequest is the payload for POST /api/chat.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatResponse is a single streamed line from /api/chat.
type ollamaChatResponse struct {
	Message         ollamaChatMessage `json:"message"`
	Done            bool              `json:"done"`
	PromptEvalCount int               `json:"prompt_eval_count"` // input tokens
	EvalCount       int               `json:"eval_count"`        // output tokens
}

// OllamaListInstalled returns model names available in the local Ollama instance.
func OllamaListInstalled() ([]string, error) {
	resp, err := http.Get("http://localhost:11434/api/tags")
	if err != nil {
		return nil, fmt.Errorf("ollama not reachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
}

// OllamaPopularModels is a curated list of models useful for coding tasks.
var OllamaPopularModels = []string{
	"kimi-k2.5:cloud",
	"qwen2.5-coder:latest",
	"deepseek-coder-v2:latest",
	"codellama:latest",
	"llama3.2:latest",
	"mistral:latest",
	"phi4:latest",
	"gemma3:latest",
}
