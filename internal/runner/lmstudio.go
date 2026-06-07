package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// LM Studio hosts an OpenAI-compatible server (started from the LM Studio app
// or via `lms server start`) on port 1234 by default. The orchestrator only
// connects to the already-running server; the user is responsible for loading
// a model in LM Studio (or LM Studio JIT-loads the requested model on the first
// request when that option is enabled).
const (
	lmStudioBaseURL   = "http://localhost:1234"
	lmStudioMaxTokens = 8192
)

// LMStudioRunner calls the LM Studio OpenAI-compatible REST API directly. The
// local model receives the prompt verbatim and is expected to return plain text
// (no agentic loop, no tool use).
type LMStudioRunner struct {
	Model   string
	BaseURL string
}

func NewLMStudioRunner(model string) *LMStudioRunner {
	return &LMStudioRunner{Model: model, BaseURL: lmStudioBaseURL}
}

func (r *LMStudioRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = r.Model
	}

	body := lmStudioChatRequest{
		Model:     model,
		Messages:  r.buildMessages(req),
		Stream:    true,
		MaxTokens: lmStudioMaxTokens,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: marshal request: %w", err)
	}

	endpoint := r.baseURL() + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("lmstudio: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: not reachable at %s (is the LM Studio server running?): %w", r.baseURL(), err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(resp.Body)
		bodyStr := strings.TrimSpace(string(errBody))
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, &RateLimitError{Provider: "lmstudio", Detail: bodyStr}
		}
		if rl := ClassifyRateLimit("lmstudio", bodyStr); rl != nil {
			return nil, rl
		}
		return nil, fmt.Errorf("lmstudio: model %q returned HTTP %d: %s", model, resp.StatusCode, bodyStr)
	}

	ch := make(chan Token, 16)
	go r.streamResponse(resp.Body, joinPromptText(req), ch)
	return ch, nil
}

func (r *LMStudioRunner) buildMessages(req CompletionRequest) []lmStudioChatMessage {
	var msgs []lmStudioChatMessage
	if req.SystemPrompt != "" {
		msgs = append(msgs, lmStudioChatMessage{
			Role:    "system",
			Content: req.SystemPrompt + "\n\nIMPORTANT: Reply with plain text only. Do NOT wrap your reply in JSON, do NOT use ```json fences, do NOT return objects like {\"response\": \"...\"}. Write the answer directly as natural prose.",
		})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, lmStudioChatMessage(m))
	}
	return msgs
}

func (r *LMStudioRunner) streamResponse(body io.ReadCloser, inputText string, ch chan<- Token) {
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		ch <- Token{Error: fmt.Errorf("lmstudio: read stream: %w", err)}
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
func (r *LMStudioRunner) streamResponseFromBytes(data []byte, inputText string, ch chan<- Token) {
	defer close(ch)

	var buf strings.Builder
	var reasoning strings.Builder
	var usage *TokenUsage
	sawChunk := false

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

		var chunk lmStudioChatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- Token{Error: fmt.Errorf("lmstudio: decode stream chunk: %w", err)}
			ch <- Token{Done: true}
			return
		}
		sawChunk = true
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				buf.WriteString(c.Delta.Content)
			}
			// Reasoning models (e.g. gemma "thinking", deepseek-r1) stream their
			// chain-of-thought in reasoning_content; track it so an answer made
			// up of reasoning only can be reported instead of looking empty.
			if c.Delta.Reasoning != "" {
				reasoning.WriteString(c.Delta.Reasoning)
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
	if output == "" {
		// LM Studio answers an over-long prompt (or a model that failed to
		// load) with HTTP 200 and an empty stream. Surface a descriptive error
		// instead of silently returning nothing, which downstream agents would
		// misreport (e.g. coder: "no file blocks found").
		ch <- Token{Error: r.emptyResponseError(inputText, sawChunk, reasoning.Len() > 0)}
		ch <- Token{Done: true}
		return
	}

	ch <- Token{Text: output}
	if usage == nil {
		usage = estimatedUsage(inputText, output)
	}
	ch <- Token{Done: true, Usage: usage}
}

// emptyResponseError builds a diagnostic error for an empty LM Studio response.
// The most common cause is a prompt that exceeds the model's loaded context
// length, so the message includes the loaded length (best-effort) and the
// estimated prompt size.
func (r *LMStudioRunner) emptyResponseError(inputText string, sawChunk, sawReasoning bool) error {
	promptTokens := tokenutil.EstimateTokens(inputText)
	if sawReasoning {
		return fmt.Errorf("lmstudio: model produced only reasoning and no answer for a ~%d-token prompt — increase the model's max_tokens / response length in LM Studio", promptTokens)
	}
	if ctxLen := r.loadedContextLength(); ctxLen > 0 && promptTokens > ctxLen {
		return fmt.Errorf("lmstudio: empty response — the prompt (~%d tokens) exceeds the model's loaded context length (%d); raise the context length in LM Studio (the model's load settings) and reload, or reduce max_context_tokens", promptTokens, ctxLen)
	}
	if !sawChunk {
		return fmt.Errorf("lmstudio: empty response for a ~%d-token prompt — the prompt may exceed the model's loaded context length, or the model failed to load in LM Studio", promptTokens)
	}
	return fmt.Errorf("lmstudio: model returned an empty answer for a ~%d-token prompt", promptTokens)
}

// loadedContextLength returns the loaded context length of the running model
// as reported by /api/v0/models, or 0 when unavailable. Best-effort.
func (r *LMStudioRunner) loadedContextLength() int {
	resp, err := http.Get(r.baseURL() + "/api/v0/models") // #nosec G107 -- fixed localhost LM Studio endpoint
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var result struct {
		Data []struct {
			ID                  string `json:"id"`
			State               string `json:"state"`
			LoadedContextLength int    `json:"loaded_context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}
	// Prefer the model this runner targets; otherwise the first loaded model.
	for _, m := range result.Data {
		if m.ID == r.Model && m.LoadedContextLength > 0 {
			return m.LoadedContextLength
		}
	}
	for _, m := range result.Data {
		if m.State == "loaded" && m.LoadedContextLength > 0 {
			return m.LoadedContextLength
		}
	}
	return 0
}

func (r *LMStudioRunner) baseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return lmStudioBaseURL
}

// lmStudioChatRequest is the OpenAI-compatible payload for /v1/chat/completions.
type lmStudioChatRequest struct {
	Model     string                `json:"model"`
	Messages  []lmStudioChatMessage `json:"messages"`
	Stream    bool                  `json:"stream"`
	MaxTokens int                   `json:"max_tokens,omitempty"`
}

type lmStudioChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// lmStudioChatChunk is a single SSE chunk from /v1/chat/completions.
type lmStudioChatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			// Reasoning models stream their chain-of-thought here.
			Reasoning string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// LMStudioListInstalled returns model IDs available in the running LM Studio
// server. It prefers the native /api/v0/models endpoint (which reports each
// model's load state, so loaded models are listed first) and falls back to the
// OpenAI-compatible /v1/models endpoint.
func LMStudioListInstalled() ([]string, error) {
	if models, err := lmStudioListV0(); err == nil && len(models) > 0 {
		return models, nil
	}
	return lmStudioListV1()
}

// lmStudioListV0 reads /api/v0/models, returning model IDs with loaded models
// first so the currently-loaded model is the default selection in setup.
func lmStudioListV0() ([]string, error) {
	resp, err := http.Get(lmStudioBaseURL + "/api/v0/models") // #nosec G107 -- fixed localhost LM Studio endpoint
	if err != nil {
		return nil, fmt.Errorf("lmstudio not reachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lmstudio /api/v0/models returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	type entry struct {
		id     string
		loaded bool
	}
	var entries []entry
	for _, m := range result.Data {
		// Skip embedding-only models — they can't serve chat completions.
		if m.Type == "embeddings" {
			continue
		}
		entries = append(entries, entry{id: m.ID, loaded: m.State == "loaded"})
	}
	// Stable sort: loaded models first, otherwise preserve server order.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].loaded && !entries[j].loaded
	})
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.id
	}
	return names, nil
}

// lmStudioListV1 reads the OpenAI-compatible /v1/models endpoint.
func lmStudioListV1() ([]string, error) {
	resp, err := http.Get(lmStudioBaseURL + "/v1/models") // #nosec G107 -- fixed localhost LM Studio endpoint
	if err != nil {
		return nil, fmt.Errorf("lmstudio not reachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lmstudio /v1/models returned HTTP %d", resp.StatusCode)
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
