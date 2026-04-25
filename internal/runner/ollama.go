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

	// ollamaMaxPredict caps generation at a generous upper bound so long
	// responses (TASKSPEC, plans, full file rewrites) are not silently
	// truncated by Ollama's per-model default (often 128 tokens), while
	// still preventing runaway loops where the model never emits EOS —
	// especially painful on memory-constrained machines.
	ollamaMaxPredict = 8192
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
		Options: ollamaOptions{
			NumPredict: ollamaMaxPredict,
		},
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
			Content: req.SystemPrompt + "\n\nIMPORTANT: Reply with plain text only. Do NOT wrap your reply in JSON, do NOT use ```json fences, do NOT return objects like {\"response\": \"...\"}. Write the answer directly as natural prose.",
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
	var buf strings.Builder
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
			buf.WriteString(chunk.Message.Content)
		}
		if chunk.Done {
			text := unwrapJSONResponse(buf.String())
			if text != "" {
				ch <- Token{Text: text}
			}
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
	if text := unwrapJSONResponse(buf.String()); text != "" {
		ch <- Token{Text: text}
	}
	ch <- Token{Done: true}
}

// unwrapJSONResponse strips a wrapping JSON envelope that some local models
// emit despite being asked for plain text, e.g. ```json {"response": "..."} ```
// or {"response": "..."} or {"reply": "..."}. Returns the original text if
// no recognized envelope is found.
func unwrapJSONResponse(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return s
	}

	trimmed = stripCodeFence(trimmed)

	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return s
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return s
	}
	for _, key := range []string{"response", "reply", "message", "content", "answer"} {
		if v, ok := envelope[key]; ok {
			if str, ok := v.(string); ok && str != "" {
				return str
			}
		}
	}
	return s
}

// stripCodeFence removes a single surrounding triple-backtick fence (with
// optional language tag) from s.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	inner := strings.TrimPrefix(s, "```")
	if nl := strings.IndexByte(inner, '\n'); nl >= 0 {
		inner = inner[nl+1:]
	}
	inner = strings.TrimSuffix(inner, "```")
	return strings.TrimSpace(inner)
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
	Options  ollamaOptions       `json:"options,omitempty"`
}

// ollamaOptions overrides Ollama's per-model generation defaults. We set
// num_predict explicitly because Ollama defaults it to 128 for many models,
// which silently truncates long responses (TASKSPEC, plans, code).
type ollamaOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
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
