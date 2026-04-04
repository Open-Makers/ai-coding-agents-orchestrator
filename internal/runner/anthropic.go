package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
)

const (
	anthropicAPIURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	defaultModel     = "claude-sonnet-4-6"
	httpTimeout      = 5 * time.Minute
)

// AnthropicRunner calls the Anthropic Messages API with streaming.
type AnthropicRunner struct {
	apiKey      string
	skillLoader *skills.Loader
	httpClient  *http.Client
	baseURL     string // override in tests
}

// NewAnthropicRunner creates a runner using the given API key.
// If skillLoader is nil, skills are silently skipped.
func NewAnthropicRunner(apiKey string, sl *skills.Loader) *AnthropicRunner {
	return &AnthropicRunner{
		apiKey:      apiKey,
		skillLoader: sl,
		httpClient:  &http.Client{Timeout: httpTimeout},
		baseURL:     anthropicAPIURL,
	}
}

// Complete implements LLMRunner via Anthropic streaming SSE.
func (r *AnthropicRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	systemPrompt := r.buildSystemPrompt(req.SystemPrompt, req.Skills)

	// Build messages array
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	if len(msgs) == 0 {
		msgs = append(msgs, map[string]string{"role": "user", "content": "."})
	}

	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 8192,
		"stream":     true,
		"system":     systemPrompt,
		"messages":   msgs,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", r.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	ch := make(chan Token, 64)
	go r.streamSSE(resp.Body, ch)
	return ch, nil
}

// streamSSE reads the SSE stream and sends tokens to ch.
func (r *AnthropicRunner) streamSSE(body io.ReadCloser, ch chan<- Token) {
	defer body.Close()
	defer close(ch)

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			ch <- Token{Done: true}
			return
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				ch <- Token{Text: event.Delta.Text}
			}
		case "message_stop":
			ch <- Token{Done: true}
			return
		case "error":
			ch <- Token{Error: fmt.Errorf("anthropic stream error: %s", data)}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- Token{Error: fmt.Errorf("anthropic: scan: %w", err)}
	} else {
		ch <- Token{Done: true}
	}
}

// buildSystemPrompt concatenates the base prompt with all skill contents.
func (r *AnthropicRunner) buildSystemPrompt(base string, skillNames []string) string {
	if len(skillNames) == 0 || r.skillLoader == nil {
		return base
	}

	var sb strings.Builder
	sb.WriteString(base)

	for _, name := range skillNames {
		content, err := r.skillLoader.Load(name)
		if err != nil || content == "" {
			continue
		}
		sb.WriteString("\n\n---\n\n")
		sb.WriteString(content)
	}

	return sb.String()
}
