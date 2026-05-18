package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// oMLX local hosting (Apple Silicon) via the oMLX OpenAI-compatible API.
// The user starts the oMLX server externally (it runs as
// `python -m omlx.cli serve --port 8000`). The orchestrator only connects
// to the already-running server and reads the API key from oMLX's own
// settings file at ~/.omlx/settings.json.
const (
	mlxBaseURL      = "http://127.0.0.1:8000"
	omlxSettingsRel = ".omlx/settings.json"

	// mlxMaxTokens caps generation so long outputs (TASKSPEC, plans, full
	// file rewrites) aren't silently truncated by the server default.
	mlxMaxTokens = 8192
)

// omlxKeyOnce loads the oMLX API key lazily on first use. Loading happens once
// per process; if oMLX is restarted with a new key, the orchestrator must be
// restarted too.
var (
	omlxKeyOnce sync.Once
	omlxKey     string
)

// loadOMLXAPIKey reads auth.api_key from ~/.omlx/settings.json. Returns empty
// string when the file is absent or the field is missing — oMLX may run with
// skip_api_key_verification=true, in which case no Authorization header is needed.
func loadOMLXAPIKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, omlxSettingsRel))
	if err != nil {
		return ""
	}
	var settings struct {
		Auth struct {
			APIKey string `json:"api_key"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ""
	}
	return strings.TrimSpace(settings.Auth.APIKey)
}

func getOMLXAPIKey() string {
	omlxKeyOnce.Do(func() { omlxKey = loadOMLXAPIKey() })
	return omlxKey
}

// MLXRunner calls the oMLX OpenAI-compatible REST API directly. The local
// model receives the prompt verbatim and is expected to return plain text
// (no agentic loop, no tool use).
type MLXRunner struct {
	Model   string
	BaseURL string
}

func NewMLXRunner(model string) *MLXRunner {
	return &MLXRunner{Model: model, BaseURL: mlxBaseURL}
}

func (r *MLXRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = r.Model
	}

	body := mlxChatRequest{
		Model:     model,
		Messages:  r.buildMessages(req),
		Stream:    true,
		MaxTokens: mlxMaxTokens,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("omlx: marshal request: %w", err)
	}

	endpoint := r.baseURL() + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("omlx: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if key := getOMLXAPIKey(); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("omlx: not reachable at %s (is oMLX running?): %w", r.baseURL(), err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(resp.Body)
		bodyStr := strings.TrimSpace(string(errBody))
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, &RateLimitError{Provider: "omlx", Detail: bodyStr}
		}
		if rl := ClassifyRateLimit("omlx", bodyStr); rl != nil {
			return nil, rl
		}
		return nil, fmt.Errorf("omlx: model %q returned HTTP %d: %s", model, resp.StatusCode, bodyStr)
	}

	ch := make(chan Token, 16)
	go r.streamResponse(resp.Body, joinPromptText(req), ch)
	return ch, nil
}

func (r *MLXRunner) buildMessages(req CompletionRequest) []mlxChatMessage {
	var msgs []mlxChatMessage
	if req.SystemPrompt != "" {
		msgs = append(msgs, mlxChatMessage{
			Role:    "system",
			Content: req.SystemPrompt + "\n\nIMPORTANT: Reply with plain text only. Do NOT wrap your reply in JSON, do NOT use ```json fences, do NOT return objects like {\"response\": \"...\"}. Write the answer directly as natural prose.",
		})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, mlxChatMessage(m))
	}
	return msgs
}

func (r *MLXRunner) streamResponse(body io.ReadCloser, inputText string, ch chan<- Token) {
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		ch <- Token{Error: fmt.Errorf("omlx: read stream: %w", err)}
		ch <- Token{Done: true}
		close(ch)
		return
	}
	r.streamResponseFromBytes(data, inputText, ch)
}

// streamResponseFromBytes parses an OpenAI-style SSE stream:
//
//	data: {"choices":[{"delta":{"content":"hi"}}], ...}
//	data: {"choices":[{"finish_reason":"stop"}], "usage": {...}}
//	data: [DONE]
func (r *MLXRunner) streamResponseFromBytes(data []byte, inputText string, ch chan<- Token) {
	defer close(ch)

	var buf strings.Builder
	var usage *TokenUsage

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk mlxChatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- Token{Error: fmt.Errorf("omlx: decode stream chunk: %w", err)}
			ch <- Token{Done: true}
			return
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				buf.WriteString(c.Delta.Content)
			}
		}
		if chunk.Usage != nil {
			usage = &TokenUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				Estimated:    false,
			}
		}
	}

	output := unwrapJSONResponse(buf.String())
	if output != "" {
		ch <- Token{Text: output}
	}
	// Most oMLX builds do not include a `usage` block in streaming responses,
	// so fall back to an estimated count instead of dropping usage entirely.
	// The monitor needs *something* to surface for local-model agents.
	if usage == nil {
		usage = estimatedUsage(inputText, output)
	}
	ch <- Token{Done: true, Usage: usage}
}

func (r *MLXRunner) baseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return mlxBaseURL
}

// mlxChatRequest is the OpenAI-compatible payload for POST /v1/chat/completions.
type mlxChatRequest struct {
	Model     string           `json:"model"`
	Messages  []mlxChatMessage `json:"messages"`
	Stream    bool             `json:"stream"`
	MaxTokens int              `json:"max_tokens,omitempty"`
}

type mlxChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// mlxChatChunk is a single SSE chunk from /v1/chat/completions.
type mlxChatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// MLXListInstalled returns model IDs reported by the running oMLX server.
func MLXListInstalled() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, mlxBaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if key := getOMLXAPIKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("omlx not reachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("omlx /v1/models returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	names := make([]string, len(result.Data))
	for i, m := range result.Data {
		names[i] = m.ID
	}
	return names, nil
}
