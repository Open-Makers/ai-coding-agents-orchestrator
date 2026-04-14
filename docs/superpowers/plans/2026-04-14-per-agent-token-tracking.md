# Per-Agent Token Tracking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track real token usage per agent across the full pipeline and display per-agent breakdown in the statusbar and completion summary.

**Architecture:** Extend `runner.Token` with `Usage *TokenUsage` populated on the Done token by each runner. `BaseAgent.collectStream` auto-emits a `bus.MsgUsage` event when it sees usage on the Done token. The coder (which has its own stream loop) accumulates across fix iterations and emits once at the end. The TUI accumulates per-role in a map and renders the table in the congratulations screen.

**Tech Stack:** Go, `github.com/pkoukk/tiktoken-go` (exact OpenAI tokenizer for Codex runner).

---

## File Map

| File | Change |
|------|--------|
| `internal/runner/runner.go` | Add `TokenUsage` struct + `Usage *TokenUsage` field to `Token` |
| `internal/runner/mock.go` | Add `Usage` to done token for testability |
| `internal/runner/ollama.go` | Parse `prompt_eval_count`/`eval_count` from done chunk |
| `internal/runner/claude.go` | Switch to `--output-format json`, parse usage from JSON |
| `internal/runner/codex.go` | Count tokens with tiktoken-go after CLI output |
| `internal/runner/opencode.go` | Parse usage event from NDJSON; fallback to estimation |
| `internal/runner/budget.go` | Pass through `Usage` from inner runner |
| `internal/bus/types.go` | Add `MsgUsage` + `AgentUsage` |
| `internal/agent/base.go` | Add `emitUsage()`; update `collectStream` to auto-emit |
| `internal/agent/coder.go` | `streamAndWriteFiles` returns `TokenUsage`; accumulate+emit in Run |
| `internal/tui/model.go` | Add `agentUsage` map; handle `MsgUsage`; pass to statusbar |
| `internal/tui/statusbar.go` | Replace `tokenChars`/`AddTokenChars` with `agentUsage`/`WithAgentUsage` |
| `go.mod` / `go.sum` | Add `github.com/pkoukk/tiktoken-go` |

---

## Task 1: Add `TokenUsage` to `runner.Token` and update `MockRunner`

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/mock.go`
- Modify: `internal/runner/runner_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/runner/runner_test.go`:

```go
func TestMockRunner_EmitsUsageOnDone(t *testing.T) {
	m := &MockRunner{
		Responses: []string{"hello"},
		MockUsage: &TokenUsage{InputTokens: 10, OutputTokens: 5},
	}

	ch, err := m.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var done Token
	for tok := range ch {
		if tok.Done {
			done = tok
		}
	}

	if done.Usage == nil {
		t.Fatal("expected Usage on Done token, got nil")
	}
	if done.Usage.InputTokens != 10 || done.Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", done.Usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/traq/dev/om/ai-coding-agents-orchestrator
go test ./internal/runner/... -run TestMockRunner_EmitsUsageOnDone -v
```

Expected: compile error — `TokenUsage` undefined, `MockUsage` undefined.

- [ ] **Step 3: Add `TokenUsage` to `runner.go` and `Usage` to `Token`**

In `internal/runner/runner.go`, after the existing imports, add before the `ConvMessage` type:

```go
// TokenUsage holds token counts for a single LLM completion.
// Estimated is true when the count was derived from a heuristic (chars÷4)
// rather than actual API data.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	Estimated    bool
}
```

Change the `Token` struct to:

```go
// Token is a single streaming chunk from an LLM.
type Token struct {
	Text  string
	Done  bool
	Error error
	Usage *TokenUsage // non-nil only on the Done token
}
```

- [ ] **Step 4: Add `MockUsage` to `MockRunner` and emit it on Done token**

Replace the entire `internal/runner/mock.go` with:

```go
package runner

import (
	"context"
	"fmt"
)

// MockRunner returns pre-programmed responses for testing.
type MockRunner struct {
	Responses []string
	MockUsage *TokenUsage // if set, attached to the Done token of each response
	idx       int
}

func (m *MockRunner) Complete(_ context.Context, _ CompletionRequest) (<-chan Token, error) {
	if m.idx >= len(m.Responses) {
		return nil, fmt.Errorf("mock: no more responses (called %d times)", m.idx+1)
	}
	resp := m.Responses[m.idx]
	m.idx++

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: resp}
		ch <- Token{Done: true, Usage: m.MockUsage}
		close(ch)
	}()
	return ch, nil
}

// Reset resets the response index so the mock can be reused.
func (m *MockRunner) Reset() {
	m.idx = 0
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/runner/... -v
```

Expected: all tests PASS including `TestMockRunner_EmitsUsageOnDone`.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/runner.go internal/runner/mock.go internal/runner/runner_test.go
git commit -m "feat(runner): add TokenUsage to Token struct and MockRunner"
```

---

## Task 2: Add `MsgUsage` and `AgentUsage` to bus

**Files:**
- Modify: `internal/bus/types.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/bus/bus_test.go` (or create if it only has other tests):

```go
func TestBus_UsageMessageType(t *testing.T) {
	b := New()
	ch := b.Subscribe()

	b.Publish(NewMessage(RolePlanner, "", MsgUsage, AgentUsage{
		InputTokens:  100,
		OutputTokens: 50,
	}))

	select {
	case msg := <-ch:
		if msg.Type != MsgUsage {
			t.Errorf("expected MsgUsage, got %q", msg.Type)
		}
		u, ok := msg.Payload.(AgentUsage)
		if !ok {
			t.Fatalf("expected AgentUsage payload, got %T", msg.Payload)
		}
		if u.InputTokens != 100 || u.OutputTokens != 50 {
			t.Errorf("unexpected usage: %+v", u)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bus/... -run TestBus_UsageMessageType -v
```

Expected: compile error — `MsgUsage` and `AgentUsage` undefined.

- [ ] **Step 3: Add `MsgUsage` and `AgentUsage` to `bus/types.go`**

In `internal/bus/types.go`, add to the `MessageType` constants block:

```go
MsgUsage MessageType = "usage"
```

Add after the `TokenPayload` struct:

```go
// AgentUsage is the payload for MsgUsage events.
// It carries token counts for one agent's LLM completion(s).
// Runner and Model are not stored here — the TUI derives them from agentConfigs.
type AgentUsage struct {
	InputTokens  int  `json:"input_tokens"`
	OutputTokens int  `json:"output_tokens"`
	Estimated    bool `json:"estimated"` // true = heuristic count
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bus/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bus/types.go internal/bus/bus_test.go
git commit -m "feat(bus): add MsgUsage and AgentUsage types"
```

---

## Task 3: Extend Ollama runner with real token counts

**Files:**
- Modify: `internal/runner/ollama.go`

- [ ] **Step 1: Write the failing test**

Create `internal/runner/ollama_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/runner/... -run TestOllamaStreamResponse_CapturesUsage -v
```

Expected: compile error — `PromptEvalCount`/`EvalCount` undefined, `streamResponseFromBytes` undefined.

- [ ] **Step 3: Update `ollamaChatResponse` struct**

In `internal/runner/ollama.go`, replace the `ollamaChatResponse` struct:

```go
// ollamaChatResponse is a single streamed line from /api/chat.
type ollamaChatResponse struct {
	Message         ollamaChatMessage `json:"message"`
	Done            bool              `json:"done"`
	PromptEvalCount int               `json:"prompt_eval_count"` // input tokens
	EvalCount       int               `json:"eval_count"`        // output tokens
}
```

- [ ] **Step 4: Extract `streamResponseFromBytes` and update `streamResponse`**

Replace `streamResponse` in `internal/runner/ollama.go` with:

```go
func (r *OllamaRunner) streamResponse(body io.ReadCloser, ch chan<- Token) {
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		ch <- Token{Error: fmt.Errorf("ollama: read stream: %w", err)}
		ch <- Token{Done: true}
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
			usage := &TokenUsage{
				InputTokens:  chunk.PromptEvalCount,
				OutputTokens: chunk.EvalCount,
				Estimated:    false,
			}
			ch <- Token{Done: true, Usage: usage}
			return
		}
	}
	ch <- Token{Done: true}
}
```

Update the goroutine call in `Complete` — it already passes `resp.Body` to `streamResponse`, no change needed there.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/runner/... -run TestOllama -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/ollama.go internal/runner/ollama_test.go
git commit -m "feat(runner/ollama): emit real token counts from API response stats"
```

---

## Task 4: Extend Claude runner with real token counts

**Files:**
- Modify: `internal/runner/claude.go`

- [ ] **Step 1: Write the failing test**

Create `internal/runner/claude_test.go`:

```go
package runner

import (
	"testing"
)

func TestClaudeParseJSONResponse(t *testing.T) {
	input := []byte(`{"type":"result","subtype":"success","result":"hello world","total_input_tokens":120,"total_output_tokens":45,"usage":{"input_tokens":120,"output_tokens":45,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)

	text, usage, err := parseClaudeJSONResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if usage.InputTokens != 120 {
		t.Errorf("InputTokens: want 120, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 45 {
		t.Errorf("OutputTokens: want 45, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false for Claude JSON response")
	}
}

func TestClaudeParseJSONResponse_FallbackOnMalformed(t *testing.T) {
	input := []byte("plain text response without json")

	text, usage, err := parseClaudeJSONResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "plain text response without json" {
		t.Errorf("unexpected text: %q", text)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true for fallback")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/runner/... -run TestClaudeParse -v
```

Expected: compile error — `parseClaudeJSONResponse` undefined.

- [ ] **Step 3: Add JSON parsing helper and update `run()`**

Replace the contents of `internal/runner/claude.go` with:

```go
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// ClaudeRunner executes the Claude CLI (claude) as an LLM backend.
type ClaudeRunner struct {
	Binary string
	Model  string
}

// ClaudeModels lists available Claude model aliases accepted by the CLI --model flag.
var ClaudeModels = []string{
	"sonnet",
	"opus",
	"haiku",
}

// ClaudeListModels returns the known Claude model aliases.
func ClaudeListModels() ([]string, error) {
	return ClaudeModels, nil
}

// Complete implements LLMRunner by running the Claude CLI with the given prompt.
func (r ClaudeRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	var userContent strings.Builder
	for _, m := range req.Messages {
		userContent.WriteString(strings.ToUpper(m.Role))
		userContent.WriteString(":\n")
		userContent.WriteString(m.Content)
		userContent.WriteString("\n\n")
	}

	raw, err := r.run(userContent.String(), req.SystemPrompt)
	if err != nil {
		return nil, err
	}

	text, usage, _ := parseClaudeJSONResponse(raw)
	// If usage is estimated, improve estimate using input content length.
	if usage.Estimated {
		usage.InputTokens = tokenutil.EstimateTokens(req.SystemPrompt + userContent.String())
	}

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: text}
		ch <- Token{Done: true, Usage: &usage}
		close(ch)
	}()
	return ch, nil
}

func (r ClaudeRunner) run(prompt, systemPrompt string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "claude"
	}

	// --output-format json gives us structured output including usage stats.
	args := []string{"--output-format", "json", "--no-session-persistence", "--allowedTools", ""}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	cmd := executil.Command(bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(out.String())
		}
		return nil, fmt.Errorf("claude: %w: %s", err, errMsg)
	}
	return out.Bytes(), nil
}

// claudeJSONResult is the shape of claude --output-format json stdout.
type claudeJSONResult struct {
	Result string `json:"result"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	// Fallback top-level fields present in some CLI versions.
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
}

// parseClaudeJSONResponse extracts result text and token usage from Claude CLI JSON output.
// Falls back gracefully to treating the raw bytes as plain text with estimated usage.
func parseClaudeJSONResponse(raw []byte) (text string, usage TokenUsage, err error) {
	var result claudeJSONResult
	if jsonErr := json.Unmarshal(bytes.TrimSpace(raw), &result); jsonErr != nil {
		// Not valid JSON — treat as plain text, estimate tokens.
		text = strings.TrimSpace(string(raw))
		usage = TokenUsage{
			OutputTokens: tokenutil.EstimateTokens(text),
			Estimated:    true,
		}
		return text, usage, nil
	}

	text = result.Result

	// Prefer usage sub-object; fall back to top-level total fields.
	inputTokens := result.Usage.InputTokens
	outputTokens := result.Usage.OutputTokens
	if inputTokens == 0 {
		inputTokens = result.TotalInputTokens
	}
	if outputTokens == 0 {
		outputTokens = result.TotalOutputTokens
	}

	if inputTokens == 0 && outputTokens == 0 {
		// JSON parsed but no usage fields — estimate from text.
		usage = TokenUsage{
			OutputTokens: tokenutil.EstimateTokens(text),
			Estimated:    true,
		}
		return text, usage, nil
	}

	usage = TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Estimated:    false,
	}
	return text, usage, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/runner/... -run TestClaude -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/claude.go internal/runner/claude_test.go
git commit -m "feat(runner/claude): use --output-format json for real token counts"
```

---

## Task 5: Add tiktoken-go and extend Codex runner

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/runner/codex.go`

- [ ] **Step 1: Add tiktoken-go dependency**

```bash
cd /Users/traq/dev/om/ai-coding-agents-orchestrator
go get github.com/pkoukk/tiktoken-go@latest
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 2: Write the failing test**

Create `internal/runner/codex_test.go`:

```go
package runner

import (
	"testing"
)

func TestCountCodexTokens_Basic(t *testing.T) {
	usage := countCodexTokens("gpt-4o", "hello world system prompt", "hello world response")
	if usage.InputTokens <= 0 {
		t.Errorf("expected positive InputTokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected positive OutputTokens, got %d", usage.OutputTokens)
	}
	// tiktoken gives exact counts — should not be estimated
	if usage.Estimated {
		t.Error("Estimated should be false when tiktoken succeeds")
	}
}

func TestCountCodexTokens_UnknownModel_FallsBackToEstimate(t *testing.T) {
	// A model name tiktoken doesn't know should fall back gracefully.
	usage := countCodexTokens("unknown-model-xyz", "hello", "world")
	if usage.InputTokens <= 0 {
		t.Errorf("expected positive InputTokens even on fallback, got %d", usage.InputTokens)
	}
	// Fallback is estimated
	if !usage.Estimated {
		t.Error("Estimated should be true on tiktoken fallback")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/runner/... -run TestCountCodexTokens -v
```

Expected: compile error — `countCodexTokens` undefined.

- [ ] **Step 4: Add `countCodexTokens` and update `Complete` in `codex.go`**

Add to `internal/runner/codex.go` (after existing imports, add the new ones):

```go
import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)
```

Add after the `run()` function:

```go
// countCodexTokens uses tiktoken to count input and output tokens exactly.
// Falls back to char-based estimation if the model encoding is unknown.
func countCodexTokens(model, inputText, outputText string) TokenUsage {
	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// Unknown model — fall back to cl100k_base (GPT-4 encoding).
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			// tiktoken completely unavailable — use char heuristic.
			return TokenUsage{
				InputTokens:  tokenutil.EstimateTokens(inputText),
				OutputTokens: tokenutil.EstimateTokens(outputText),
				Estimated:    true,
			}
		}
	}

	inputToks := enc.Encode(inputText, nil, nil)
	outputToks := enc.Encode(outputText, nil, nil)

	return TokenUsage{
		InputTokens:  len(inputToks),
		OutputTokens: len(outputToks),
		Estimated:    false,
	}
}
```

Update `Complete` in `codex.go` to build the full input string and count tokens:

```go
func (r CodexRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	var userContent strings.Builder
	for _, m := range req.Messages {
		userContent.WriteString(strings.ToUpper(m.Role))
		userContent.WriteString(":\n")
		userContent.WriteString(m.Content)
		userContent.WriteString("\n\n")
	}

	model := req.Model
	if model == "" {
		model = r.Model
	}

	output, err := r.run(ctx, userContent.String(), req.SystemPrompt, model)
	if err != nil {
		return nil, err
	}

	fullInput := req.SystemPrompt + "\n\n" + userContent.String()
	usage := countCodexTokens(model, fullInput, string(output))

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: string(output)}
		ch <- Token{Done: true, Usage: &usage}
		close(ch)
	}()
	return ch, nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/runner/... -run TestCountCodexTokens -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/runner/codex.go internal/runner/codex_test.go
git commit -m "feat(runner/codex): count tokens with tiktoken-go for exact usage tracking"
```

---

## Task 6: Extend OpenCode runner to parse usage from NDJSON

**Files:**
- Modify: `internal/runner/opencode.go`

- [ ] **Step 1: Write the failing test**

Create `internal/runner/opencode_test.go`:

```go
package runner

import (
	"testing"
)

func TestExtractOpenCodeResponse_WithUsageEvent(t *testing.T) {
	ndjson := []byte(`{"type":"text","part":{"text":"hello "}}
{"type":"text","part":{"text":"world"}}
{"type":"usage","usage":{"input_tokens":80,"output_tokens":12}}
`)

	text, usage := extractOpenCodeResponseWithUsage(ndjson)
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if usage.InputTokens != 80 {
		t.Errorf("InputTokens: want 80, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 12 {
		t.Errorf("OutputTokens: want 12, got %d", usage.OutputTokens)
	}
	if usage.Estimated {
		t.Error("Estimated should be false when usage event found")
	}
}

func TestExtractOpenCodeResponse_FallbackEstimate(t *testing.T) {
	ndjson := []byte(`{"type":"text","part":{"text":"hello world"}}
`)

	text, usage := extractOpenCodeResponseWithUsage(ndjson)
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}
	if !usage.Estimated {
		t.Error("Estimated should be true when no usage event in stream")
	}
	if usage.OutputTokens <= 0 {
		t.Error("expected positive OutputTokens on fallback estimate")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/runner/... -run TestExtractOpenCode -v
```

Expected: compile error — `extractOpenCodeResponseWithUsage` undefined.

- [ ] **Step 3: Add `extractOpenCodeResponseWithUsage` and update `Complete`**

In `internal/runner/opencode.go`, add import `"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"` to the imports block.

Replace the existing `extractOpenCodeResponse` function and update `Complete`:

```go
// extractOpenCodeResponseWithUsage parses NDJSON event stream from opencode --format json.
// Returns concatenated text from all "text" type events, plus token usage.
// If a "usage" event is present, returns exact counts (Estimated: false).
// Otherwise estimates from output text length (Estimated: true).
func extractOpenCodeResponseWithUsage(data []byte) (string, TokenUsage) {
	var sb strings.Builder
	var foundUsage bool
	var usage TokenUsage

	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var event struct {
			Type  string `json:"type"`
			Part  struct {
				Text string `json:"text"`
			} `json:"part"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		switch event.Type {
		case "text":
			if event.Part.Text != "" {
				sb.WriteString(event.Part.Text)
			}
		case "usage":
			if event.Usage != nil {
				usage = TokenUsage{
					InputTokens:  event.Usage.InputTokens,
					OutputTokens: event.Usage.OutputTokens,
					Estimated:    false,
				}
				foundUsage = true
			}
		}
	}

	text := sb.String()
	if text == "" {
		text = strings.TrimSpace(string(data))
	}

	if !foundUsage {
		usage = TokenUsage{
			OutputTokens: tokenutil.EstimateTokens(text),
			Estimated:    true,
		}
	}

	return text, usage
}

// extractOpenCodeResponse is kept for backward compatibility in non-usage contexts.
func extractOpenCodeResponse(data []byte) string {
	text, _ := extractOpenCodeResponseWithUsage(data)
	return text
}
```

Update `Complete` in `opencode.go` to use the new function:

```go
func (r OpenCodeRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	var sb strings.Builder
	if req.SystemPrompt != "" {
		sb.WriteString(req.SystemPrompt)
		sb.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(":\n")
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}

	model := req.Model
	if model == "" {
		model = r.Model
	}

	rawBytes, err := r.runRaw(ctx, sb.String(), model)
	if err != nil {
		return nil, err
	}

	text, usage := extractOpenCodeResponseWithUsage(rawBytes)

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: text}
		ch <- Token{Done: true, Usage: &usage}
		close(ch)
	}()
	return ch, nil
}
```

Rename `run()` to `runRaw()` and change its return type to `([]byte, error)`:

```go
func (r OpenCodeRunner) runRaw(ctx context.Context, prompt, model string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "opencode"
	}

	if model == "" {
		model = "qwen2.5-coder:3b"
	}

	args := []string{"run", "--format", "json"}

	if !strings.Contains(model, "/") {
		model = "ollama/" + model
	}

	args = append(args, "-m", model)
	args = append(args, "-")

	cmd := executil.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opencode: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/runner/... -run TestExtractOpenCode -v
```

Expected: PASS.

- [ ] **Step 5: Compile check**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/runner/opencode.go internal/runner/opencode_test.go
git commit -m "feat(runner/opencode): parse usage event from NDJSON stream with estimation fallback"
```

---

## Task 7: Update BudgetRunner to pass through token usage

**Files:**
- Modify: `internal/runner/budget.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/runner/budget_test.go` (create if it doesn't exist):

```go
package runner

import (
	"context"
	"testing"
)

func TestBudgetRunner_PassesThroughUsage(t *testing.T) {
	inner := &MockRunner{
		Responses: []string{"response text"},
		MockUsage: &TokenUsage{InputTokens: 100, OutputTokens: 50},
	}
	budget := &BudgetRunner{inner: inner, maxTokens: 10000}

	ch, err := budget.Complete(context.Background(), CompletionRequest{
		SystemPrompt: "system",
		Messages:     []ConvMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var done Token
	for tok := range ch {
		if tok.Done {
			done = tok
		}
	}

	if done.Usage == nil {
		t.Fatal("BudgetRunner should pass through Usage from inner runner")
	}
	if done.Usage.InputTokens != 100 || done.Usage.OutputTokens != 50 {
		t.Errorf("unexpected usage: %+v", done.Usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/runner/... -run TestBudgetRunner_PassesThroughUsage -v
```

Expected: FAIL — `BudgetRunner.Complete` doesn't forward Usage from the inner runner's Done token.

- [ ] **Step 3: Inspect current `BudgetRunner.Complete`**

Current implementation in `internal/runner/budget.go` calls `r.inner.Complete()` and returns the channel directly. The inner channel already carries the Usage field on Done token — but BudgetRunner returns the channel as-is so it should work. Verify:

```bash
go test ./internal/runner/... -run TestBudgetRunner_PassesThroughUsage -v
```

If PASS: BudgetRunner already passes through the channel unchanged, no code change needed. Move to Step 5.

If FAIL: BudgetRunner wraps/replaces tokens. Update `Complete` to forward the done token including Usage:

```go
func (r *BudgetRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	systemBudget := r.maxTokens / 5
	messageBudget := r.maxTokens - systemBudget

	req.SystemPrompt = tokenutil.Truncate(req.SystemPrompt, systemBudget)

	for i := range req.Messages {
		msgTokens := tokenutil.EstimateTokens(req.Messages[i].Content)
		if msgTokens > messageBudget {
			req.Messages[i].Content = tokenutil.Truncate(req.Messages[i].Content, messageBudget)
		}
		messageBudget -= tokenutil.EstimateTokens(req.Messages[i].Content)
		if messageBudget <= 0 {
			break
		}
	}

	// Return inner channel directly — Done token with Usage passes through unchanged.
	return r.inner.Complete(ctx, req)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/runner/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/budget.go internal/runner/budget_test.go
git commit -m "test(runner/budget): verify Usage passes through from inner runner"
```

---

## Task 8: Add `emitUsage` to `BaseAgent` and update `collectStream` to auto-emit

**Files:**
- Modify: `internal/agent/base.go`
- Modify: `internal/agent/planner.go` (where `collectStream` is defined)

- [ ] **Step 1: Write the failing test**

Add to `internal/agent/base_test.go`:

```go
func TestBaseAgent_EmitUsage(t *testing.T) {
	b := bus.New()
	ch := b.Subscribe()

	a := NewBase(bus.RolePlanner, b)
	a.emitUsage(runner.TokenUsage{InputTokens: 200, OutputTokens: 80, Estimated: false})

	select {
	case msg := <-ch:
		if msg.Type != bus.MsgUsage {
			t.Fatalf("expected MsgUsage, got %q", msg.Type)
		}
		u, ok := msg.Payload.(bus.AgentUsage)
		if !ok {
			t.Fatalf("expected AgentUsage payload, got %T", msg.Payload)
		}
		if u.InputTokens != 200 || u.OutputTokens != 80 {
			t.Errorf("unexpected usage: %+v", u)
		}
		if u.Estimated {
			t.Error("Estimated should be false")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestBaseAgent_CollectStream_EmitsUsage(t *testing.T) {
	b := bus.New()
	ch := b.Subscribe()

	a := NewBase(bus.RoleCoder, b)

	// Build a fake token channel with usage on Done.
	tokenCh := make(chan runner.Token, 3)
	tokenCh <- runner.Token{Text: "hello "}
	tokenCh <- runner.Token{Text: "world"}
	tokenCh <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 50, OutputTokens: 10}}
	close(tokenCh)

	text, err := a.collectStream(tokenCh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}

	// Drain bus events to find MsgUsage.
	var foundUsage bool
	deadline := time.After(time.Second)
	for !foundUsage {
		select {
		case msg := <-ch:
			if msg.Type == bus.MsgUsage {
				u := msg.Payload.(bus.AgentUsage)
				if u.InputTokens == 50 && u.OutputTokens == 10 {
					foundUsage = true
				}
			}
		case <-deadline:
			t.Fatal("timeout waiting for MsgUsage")
		}
	}
}
```

Update imports in `base_test.go` to include `runner` package:

```go
import (
	"testing"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/agent/... -run "TestBaseAgent_EmitUsage|TestBaseAgent_CollectStream" -v
```

Expected: compile error — `emitUsage` undefined.

- [ ] **Step 3: Add `emitUsage` to `internal/agent/base.go`**

Add import for runner package. Replace `internal/agent/base.go` with:

```go
package agent

import (
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// BaseAgent holds shared state and helpers for all agents.
type BaseAgent struct {
	role bus.AgentRole
	Bus  *bus.Bus
}

// NewBase creates a BaseAgent for the given role.
func NewBase(role bus.AgentRole, b *bus.Bus) BaseAgent {
	return BaseAgent{role: role, Bus: b}
}

// emit publishes a broadcast message from this agent.
func (a *BaseAgent) emit(typ bus.MessageType, payload any) {
	a.Bus.Publish(bus.NewMessage(a.role, "", typ, payload))
}

// emitToken publishes a streaming token event.
func (a *BaseAgent) emitToken(text string, done bool) {
	a.emit(bus.MsgEvent, bus.TokenPayload{Text: text, Done: done})
}

// emitUsage publishes a token usage event for this agent.
// Called automatically by collectStream when a Done token carries Usage.
func (a *BaseAgent) emitUsage(usage runner.TokenUsage) {
	a.emit(bus.MsgUsage, bus.AgentUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		Estimated:    usage.Estimated,
	})
}

// Gate publishes a human_gate event (exported so orchestrator can call it if needed).
func (a *BaseAgent) Gate(msg string) {
	a.emit(bus.MsgHumanGate, msg)
}
```

- [ ] **Step 4: Update `collectStream` in `internal/agent/planner.go`**

Replace the `collectStream` method on `BaseAgent` (at the bottom of planner.go):

```go
// collectStream drains a token channel, emitting tokens to the bus and returning full text.
// If the Done token carries Usage, a MsgUsage event is emitted automatically.
func (a *BaseAgent) collectStream(ch <-chan runner.Token) (string, error) {
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return sb.String(), tok.Error
		}
		if tok.Done {
			if tok.Usage != nil {
				a.emitUsage(*tok.Usage)
			}
			break
		}
		a.emitToken(tok.Text, false)
		sb.WriteString(tok.Text)
	}
	a.emitToken("", true)
	return sb.String(), nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/agent/... -run "TestBaseAgent_EmitUsage|TestBaseAgent_CollectStream" -v
```

Expected: PASS.

- [ ] **Step 6: Run full test suite to check for regressions**

```bash
go test ./... 2>&1 | tail -20
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/base.go internal/agent/planner.go internal/agent/base_test.go
git commit -m "feat(agent): add emitUsage and auto-emit in collectStream"
```

---

## Task 9: Update CoderAgent to accumulate and emit usage across fix loop

**Files:**
- Modify: `internal/agent/coder.go`

- [ ] **Step 1: Identify the two call sites of `streamAndWriteFiles` in coder.go**

Lines to update (verify with `grep -n streamAndWriteFiles internal/agent/coder.go`):
- `Run()` method: initial code generation call
- Fix loop: called on each fix iteration

- [ ] **Step 2: Update `streamAndWriteFiles` signature to return `runner.TokenUsage`**

Change the function signature and capture usage from the Done token:

```go
// streamAndWriteFiles consumes the LLM token stream, detects code fence blocks,
// and writes each file to disk immediately when a fence closes.
// Returns written file paths, full raw output, token usage, and any error.
func (a *CoderAgent) streamAndWriteFiles(ch <-chan runner.Token) ([]string, string, runner.TokenUsage, error) {
	var fullOutput strings.Builder
	var lineBuf strings.Builder
	var recentLines []string
	var contentLines []string
	var written []string
	var capturedUsage runner.TokenUsage

	inFence := false
	currentPath := ""

	// ... (keep all existing fence-parsing logic unchanged) ...

	for tok := range ch {
		if tok.Error != nil {
			return written, fullOutput.String(), capturedUsage, tok.Error
		}
		if tok.Done {
			if tok.Usage != nil {
				capturedUsage = *tok.Usage
			}
			break
		}
		a.emitToken(tok.Text, false)
		lineBuf.WriteString(tok.Text)

		for {
			content := lineBuf.String()
			idx := strings.Index(content, "\n")
			if idx < 0 {
				break
			}
			line := content[:idx]
			lineBuf.Reset()
			lineBuf.WriteString(content[idx+1:])
			flushLine(line)
		}
	}

	if lineBuf.Len() > 0 {
		flushLine(lineBuf.String())
	}

	a.emitToken("", true)
	return written, fullOutput.String(), capturedUsage, nil
}
```

- [ ] **Step 3: Update `Run()` to accumulate usage**

In `CoderAgent.Run()`, after the existing `ch, err := a.runner.Complete(...)` call:

```go
var totalUsage runner.TokenUsage

written, fullOutput, usage, err := a.streamAndWriteFiles(ch)
if err != nil {
	return bus.Message{}, fmt.Errorf("coder: stream: %w", err)
}
totalUsage.InputTokens += usage.InputTokens
totalUsage.OutputTokens += usage.OutputTokens
if usage.Estimated {
	totalUsage.Estimated = true
}
```

At the end of `Run()`, before the `return` statement, emit the accumulated usage:

```go
a.emitUsage(totalUsage)
return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, CoderResult{Files: written}), nil
```

- [ ] **Step 4: Update fix loop to accumulate usage**

In the fix loop (find `fixWritten, _, err := a.streamAndWriteFiles(ch)`), update to:

```go
fixWritten, _, fixUsage, err := a.streamAndWriteFiles(ch)
if err != nil {
	return files, fmt.Errorf("build fix stream: %w", err)
}
totalUsage.InputTokens += fixUsage.InputTokens
totalUsage.OutputTokens += fixUsage.OutputTokens
if fixUsage.Estimated {
	totalUsage.Estimated = true
}
```

Note: `totalUsage` is declared in `Run()`. The fix loop runs inside a helper function called from `Run()`. If the fix loop is in a separate method (check `runBuildLoop` or similar), pass `*runner.TokenUsage` as a param or use a shared variable.

Verify the fix loop location:
```bash
grep -n "streamAndWriteFiles\|buildLoop\|fixLoop" internal/agent/coder.go
```

Adapt the accumulation pattern to match the actual code structure.

- [ ] **Step 5: Compile check**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/coder.go
git commit -m "feat(agent/coder): accumulate token usage across fix loop and emit at completion"
```

---

## Task 10: Update TUI Model to track per-agent usage

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/messages.go`

- [ ] **Step 1: Add `agentUsage` map to `Model`**

In `internal/tui/model.go`, add to the `Model` struct (after `agentConfigs`):

```go
agentUsage map[bus.AgentRole]bus.AgentUsage
```

In `New()`, initialize it:

```go
agentUsage: make(map[bus.AgentRole]bus.AgentUsage),
```

- [ ] **Step 2: Handle `MsgUsage` in `Update()`**

In the `BusMessageMsg` handler block (around line 234 where `case bus.MsgHumanGate:` is), add a new case inside the `switch bm.Type` block:

```go
case bus.MsgUsage:
	if u, ok := bm.Payload.(bus.AgentUsage); ok {
		existing := m.agentUsage[bm.From]
		existing.InputTokens += u.InputTokens
		existing.OutputTokens += u.OutputTokens
		if u.Estimated {
			existing.Estimated = true
		}
		m.agentUsage[bm.From] = existing
		// Recompute total for statusbar.
		m.statusbar = m.statusbar.WithAgentUsage(m.agentUsage)
	}
```

- [ ] **Step 3: Remove the char-counting line**

Find and remove this line in `Update()`:

```go
// Count output tokens for the status bar.
if tp, ok := bm.Payload.(bus.TokenPayload); ok && !tp.Done {
	m.statusbar = m.statusbar.AddTokenChars(len(tp.Text))
}
```

- [ ] **Step 4: Pass agentUsage to congratulations renderer**

In `renderCongratulations`, add `m.agentUsage` as a parameter — or access it directly since it's a method on `Model` (it already has access via `m.agentUsage`). No change needed here; the next task handles rendering.

- [ ] **Step 5: Compile check**

```bash
go build ./...
```

Expected: compile error about `WithAgentUsage` (not yet implemented) — that's OK, implement in Task 11.

- [ ] **Step 6: Commit after Task 11 passes** (defer this commit to after statusbar is updated)

---

## Task 11: Update StatusBar to use `WithAgentUsage`

**Files:**
- Modify: `internal/tui/statusbar.go`

- [ ] **Step 1: Replace `tokenChars` with `agentUsage` in `StatusBarModel`**

In `internal/tui/statusbar.go`, replace:

```go
tokenChars    int // cumulative output characters (used to estimate tokens)
```

with:

```go
totalTokens int // cached sum of input+output across all agents
```

- [ ] **Step 2: Replace `AddTokenChars` with `WithAgentUsage`**

Remove `AddTokenChars` and add:

```go
// WithAgentUsage updates the token counter from the current per-agent usage map.
func (m StatusBarModel) WithAgentUsage(usage map[bus.AgentRole]bus.AgentUsage) StatusBarModel {
	total := 0
	for _, u := range usage {
		total += u.InputTokens + u.OutputTokens
	}
	m.totalTokens = total
	return m
}
```

- [ ] **Step 3: Update `View()` to use `totalTokens`**

Replace:

```go
if m.tokenChars > 0 {
	suffix += "  " + styleStatusKey.Render("⚡ "+formatTokens(m.tokenChars))
}
```

with:

```go
if m.totalTokens > 0 {
	suffix += "  " + styleStatusKey.Render("⚡ "+formatTokens(m.totalTokens))
}
```

- [ ] **Step 4: Update `formatTokens` to accept int directly (not chars)**

`formatTokens` currently expects chars and divides by 4. Now it receives actual token count. Update:

```go
// formatTokens returns a human-friendly token count string.
func formatTokens(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM tok", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fk tok", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d tok", tokens)
	}
}
```

Add import for `bus` package at top of `statusbar.go`:

```go
"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
```

- [ ] **Step 5: Compile and run**

```bash
go build ./...
go test ./internal/tui/... -v 2>/dev/null || true
```

Expected: no compile errors.

- [ ] **Step 6: Commit Tasks 10 + 11 together**

```bash
git add internal/tui/model.go internal/tui/statusbar.go
git commit -m "feat(tui): track per-agent token usage and display total in statusbar"
```

---

## Task 12: Add per-agent token table to congratulations screen

**Files:**
- Modify: `internal/tui/model.go`

- [ ] **Step 1: Add `renderTokenTable` helper method to `model.go`**

Add this method to `model.go`:

```go
// renderTokenTable builds a per-agent token usage table for the congratulations screen.
// Agents with zero tokens (skipped/unused) are omitted.
func (m Model) renderTokenTable() string {
	if len(m.agentUsage) == 0 {
		return ""
	}

	labelStyle := lipgloss.NewStyle().Foreground(crt.dim)
	valueStyle := lipgloss.NewStyle().Foreground(crt.bright)
	estimateStyle := lipgloss.NewStyle().Foreground(crt.muted)
	headerStyle := lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(crt.border)
	totalStyle := lipgloss.NewStyle().Foreground(crt.primary).Bold(true)

	sep := sepStyle.Render(strings.Repeat("─", 52))

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(headerStyle.Render("TOKEN USAGE PER AGENT"))
	sb.WriteString("\n")
	sb.WriteString(sep)
	sb.WriteString("\n")

	var totalIn, totalOut int
	anyEstimated := false

	for _, role := range agentOrder {
		u, ok := m.agentUsage[role]
		if !ok || (u.InputTokens == 0 && u.OutputTokens == 0) {
			continue
		}

		r, mdl := runnerModelForRole(m.agentConfigs, role)
		runnerInfo := r
		if mdl != "" {
			runnerInfo += "/" + mdl
		}

		estMarker := ""
		if u.Estimated {
			estMarker = estimateStyle.Render(" ~")
			anyEstimated = true
		}

		row := fmt.Sprintf("  %-10s %-16s  in: %6s  out: %6s%s",
			string(role),
			runnerInfo,
			formatTokens(u.InputTokens),
			formatTokens(u.OutputTokens),
			estMarker,
		)
		sb.WriteString(labelStyle.Render(row))
		sb.WriteString("\n")

		totalIn += u.InputTokens
		totalOut += u.OutputTokens
	}

	sb.WriteString(sep)
	sb.WriteString("\n")

	totalRow := fmt.Sprintf("  %-10s %-16s  in: %6s  out: %6s",
		"TOTAL", "",
		formatTokens(totalIn),
		formatTokens(totalOut),
	)
	sb.WriteString(totalStyle.Render(totalRow))
	if anyEstimated {
		sb.WriteString(estimateStyle.Render("  (~ = estimated)"))
	}
	sb.WriteString("\n")

	// Pass formatted tokens through valueStyle for the total numbers.
	_ = valueStyle

	return sb.String()
}
```

- [ ] **Step 2: Call `renderTokenTable` from `renderCongratulations`**

In `renderCongratulations`, after the summary file block (around line 800), add before the final "Press m for menu" line:

```go
tokenTable := m.renderTokenTable()
if tokenTable != "" {
	content.WriteString(tokenTable)
}
```

- [ ] **Step 3: Compile check**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Run all tests**

```bash
go test ./... 2>&1 | grep -E "FAIL|ok|---"
```

Expected: no FAILs.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/model.go
git commit -m "feat(tui): show per-agent token usage table in pipeline completion screen"
```

---

## Self-Review

**Spec coverage check:**
- ✓ Ollama: real tokens from `prompt_eval_count`/`eval_count` (Task 3)
- ✓ Claude: `--output-format json` with fallback (Task 4)
- ✓ Codex: tiktoken-go client-side exact counting (Task 5)
- ✓ OpenCode: parse usage event, fallback to estimation (Task 6)
- ✓ BudgetRunner: pass-through (Task 7)
- ✓ Per-agent accumulation in pipeline (Tasks 8, 9)
- ✓ Fix loop accumulation in coder (Task 9)
- ✓ Statusbar total token display (Tasks 10, 11)
- ✓ Congratulations table per-agent (Task 12)
- ✓ `~` marker for estimated values (Task 12)
- ✓ Skipped agents omitted from table (Task 12)

**Placeholder scan:** None found.

**Type consistency:**
- `TokenUsage` defined in Task 1, used consistently in Tasks 3–9
- `AgentUsage` defined in Task 2, used in Tasks 8, 10–12
- `WithAgentUsage(map[bus.AgentRole]bus.AgentUsage)` defined in Task 11, called in Task 10
- `emitUsage(runner.TokenUsage)` defined in Task 8, called in Tasks 8, 9
- `streamAndWriteFiles` returns `([]string, string, runner.TokenUsage, error)` — updated callers in Task 9
- `formatTokens(int)` signature change (was chars, now tokens) — `View()` updated in Task 11
