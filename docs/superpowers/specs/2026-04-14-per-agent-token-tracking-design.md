# Per-Agent Token Tracking — Design Spec

**Date:** 2026-04-14  
**Status:** Approved

## Goal

Track real token usage per agent across the pipeline. Show per-agent breakdown in the status bar and in the pipeline completion summary. Accumulate across the full pipeline run (including fix loops).

## Data Sources Per Runner

| Runner    | Method                                  | Accuracy     |
|-----------|-----------------------------------------|--------------|
| Ollama    | `prompt_eval_count` / `eval_count` from stream done chunk | Exact |
| Claude    | `--output-format json` CLI response with `usage.*` fields | Exact |
| Codex     | `tiktoken-go` client-side tokenizer, applied to input/output text | Exact |
| OpenCode  | Parse NDJSON for usage event; fallback to chars÷4 if absent | Exact or `~` |

`Estimated: bool` flag on `TokenUsage` marks heuristic values in the UI with `~`.

---

## Architecture

### Approach: Token In-Band (Approach A)

Usage data rides the existing `<-chan Token` channel. Runners populate `Token.Usage` on the `Done` token. Agents read it and emit a new bus message. TUI accumulates per-role.

```
runner.Complete() → Token{Done:true, Usage: &TokenUsage{...}}
                         ↓
BaseAgent reads done token → emitUsage() → bus.MsgUsage{AgentUsage}
                                                  ↓
                                     TUI: map[AgentRole]AgentUsage
```

---

## Data Structures

### `internal/runner/runner.go`

```go
type TokenUsage struct {
    InputTokens  int
    OutputTokens int
    Estimated    bool // true = heuristic (chars÷4), false = from API/tokenizer
}

type Token struct {
    Text  string
    Done  bool
    Error error
    Usage *TokenUsage // non-nil only on the Done token
}
```

### `internal/bus/types.go`

```go
const MsgUsage MessageType = "usage"

type AgentUsage struct {
    Runner       string
    Model        string
    InputTokens  int
    OutputTokens int
    Estimated    bool
}
```

`AgentUsage.Role` is not stored — it is carried by `bus.Message.From`.

---

## Runner Changes

### Ollama (`internal/runner/ollama.go`)

Extend `ollamaChatResponse` to capture stats from the done chunk:

```go
type ollamaChatResponse struct {
    Message         ollamaChatMessage `json:"message"`
    Done            bool              `json:"done"`
    PromptEvalCount int               `json:"prompt_eval_count"`
    EvalCount       int               `json:"eval_count"`
}
```

In `streamResponse`, when `chunk.Done == true`, emit:

```go
ch <- Token{
    Done: true,
    Usage: &TokenUsage{
        InputTokens:  chunk.PromptEvalCount,
        OutputTokens: chunk.EvalCount,
        Estimated:    false,
    },
}
```

### Claude (`internal/runner/claude.go`)

Replace `--print` with `--output-format json`. Parse the JSON response:

```json
{
  "type": "result",
  "result": "...",
  "usage": {
    "input_tokens": 1234,
    "output_tokens": 567,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0
  }
}
```

Emit `Token{Text: result, Done: true, Usage: &TokenUsage{InputTokens: ..., OutputTokens: ..., Estimated: false}}`.

### Codex (`internal/runner/codex.go`)

Keep `codex exec --full-auto` CLI call unchanged. After receiving the output text, use `tiktoken-go` to count:

- `InputTokens`: tokenize `systemPrompt + messages` (same encoding as the model)
- `OutputTokens`: tokenize response text

```go
import "github.com/pkoukk/tiktoken-go"

enc, _ := tiktoken.EncodingForModel(model)
inputTokens := len(enc.Encode(fullPrompt, nil, nil))
outputTokens := len(enc.Encode(responseText, nil, nil))
```

`Estimated: false` — tiktoken gives identical counts to OpenAI billing.

### OpenCode (`internal/runner/opencode.go`)

Extend `extractOpenCodeResponse` to also look for a usage event:

```go
var event struct {
    Type  string `json:"type"`
    Part  struct{ Text string `json:"text"` } `json:"part"`
    Usage struct {
        InputTokens  int `json:"input_tokens"`
        OutputTokens int `json:"output_tokens"`
    } `json:"usage"`
}
```

If a usage event is found → `Estimated: false`. Otherwise estimate from output char length → `Estimated: true`.

### BudgetRunner (`internal/runner/budget.go`)

Pass through `Usage` from inner runner on the done token transparently.

---

## Agent Layer (`internal/agent/base.go`)

New method on `BaseAgent`:

```go
func (a *BaseAgent) emitUsage(usage *runner.TokenUsage, runnerName, model string) {
    a.emit(bus.MsgUsage, bus.AgentUsage{
        Runner:       runnerName,
        Model:        model,
        InputTokens:  usage.InputTokens,
        OutputTokens: usage.OutputTokens,
        Estimated:    usage.Estimated,
    })
}
```

Each agent captures usage from the done token at the end of its completion loop:

```go
if tok.Done {
    if tok.Usage != nil {
        a.emitUsage(tok.Usage, runnerName, model)
    }
    break
}
```

**Coder fix loop:** accumulate locally across all LLM calls (initial + fix iterations), emit once after the full loop ends:

```go
var totalUsage runner.TokenUsage
// inside fix loop:
if tok.Usage != nil {
    totalUsage.InputTokens  += tok.Usage.InputTokens
    totalUsage.OutputTokens += tok.Usage.OutputTokens
    if tok.Usage.Estimated {
        totalUsage.Estimated = true // any estimated → whole is estimated
    }
}
// after loop:
a.emitUsage(&totalUsage, runnerName, model)
```

---

## TUI Changes

### `internal/tui/model.go`

Add to `Model`:

```go
agentUsage map[bus.AgentRole]bus.AgentUsage
```

Initialize in `New()`:

```go
agentUsage: make(map[bus.AgentRole]bus.AgentUsage),
```

Handle `MsgUsage` in `Update()`:

```go
case bus.MsgUsage:
    if u, ok := bm.Payload.(bus.AgentUsage); ok {
        existing := m.agentUsage[bm.From]
        existing.InputTokens  += u.InputTokens
        existing.OutputTokens += u.OutputTokens
        existing.Runner = u.Runner
        existing.Model  = u.Model
        if u.Estimated {
            existing.Estimated = true
        }
        m.agentUsage[bm.From] = existing
    }
```

Pass `agentUsage` to status bar when rendering:

```go
m.statusbar = m.statusbar.WithAgentUsage(m.agentUsage)
```

### `internal/tui/statusbar.go`

Replace `tokenChars int` with:

```go
agentUsage map[bus.AgentRole]bus.AgentUsage
totalTokens int // cached sum, recomputed on WithAgentUsage
```

`formatTokens` uses `totalTokens` (input + output sum). Display unchanged: `⚡ 12.3k tok`.

### `internal/tui/model.go` — `renderCongratulations`

Append a per-agent token table after the existing summary:

```
TOKEN USAGE PER AGENT
─────────────────────────────────────────────────
  pm        opencode/qwen   in:  2.1k  out:   847
  planner   claude/sonnet   in:  8.4k  out:  2.3k
  coder     ollama/qwen     in: 45.2k  out: 12.1k
  reviewer  claude/sonnet   in:  6.1k  out:   934
─────────────────────────────────────────────────
  TOTAL                     in: 61.8k  out: 16.2k
```

`~` suffix on any row where `AgentUsage.Estimated == true`.

Only agents with `InputTokens > 0 || OutputTokens > 0` are shown (skipped agents omitted).

---

## Dependencies

- Add `github.com/pkoukk/tiktoken-go` for Codex token counting.

---

## Out of Scope

- Cost calculation in USD (no pricing table added)
- Persistent storage of usage across sessions
- Cache token breakdown (cache_creation / cache_read) — tracked at runner level but not displayed
