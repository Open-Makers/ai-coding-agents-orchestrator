package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
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

// Complete implements LLMRunner by streaming the Claude CLI output incrementally.
// Uses `--output-format stream-json --verbose` so the TUI shows progress live
// instead of a single burst at the end of generation.
func (r ClaudeRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	prompt := buildClaudePrompt(req.Messages)

	cmd, stdout, stderr, err := r.startStreamingProcess(ctx, prompt, req.SystemPrompt)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 16)
	go streamClaudeOutput(cmd, stdout, stderr, req, prompt, ch)
	return ch, nil
}

func buildClaudePrompt(messages []ConvMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(":\n")
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func (r ClaudeRunner) startStreamingProcess(ctx context.Context, prompt, systemPrompt string) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	bin := r.Binary
	if bin == "" {
		bin = "claude"
	}

	args := []string{"--output-format", "stream-json", "--verbose", "--no-session-persistence", "--allowedTools", ""}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	cmd := executil.Command(bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("claude: stdout pipe: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("claude: start: %w", err)
	}

	// Best-effort: stop the process if the caller's context is cancelled.
	if ctx != nil {
		go func() {
			<-ctx.Done()
			_ = cmd.Process.Kill()
		}()
	}

	return cmd, stdout, stderr, nil
}

// streamClaudeOutput parses NDJSON events from the Claude CLI and emits
// Tokens incrementally. Each `assistant` event carries the cumulative
// message text; we emit only the new suffix so the channel sees deltas.
func streamClaudeOutput(cmd *exec.Cmd, stdout io.ReadCloser, stderr *bytes.Buffer, req CompletionRequest, prompt string, ch chan<- Token) {
	defer close(ch)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var emitted strings.Builder
	var finalUsage TokenUsage
	gotUsage := false

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		evt, err := parseClaudeStreamEvent(line)
		if err != nil {
			continue
		}
		switch evt.Type {
		case "assistant":
			text := assistantText(evt.Message.Content)
			delta := computeDelta(emitted.String(), text)
			if delta != "" {
				ch <- Token{Text: delta}
				emitted.WriteString(delta)
			}
		case "result":
			if evt.Result != "" {
				delta := computeDelta(emitted.String(), evt.Result)
				if delta != "" {
					ch <- Token{Text: delta}
					emitted.WriteString(delta)
				}
			}
			if evt.Usage.InputTokens > 0 || evt.Usage.OutputTokens > 0 {
				finalUsage = TokenUsage{
					InputTokens:  evt.Usage.InputTokens,
					OutputTokens: evt.Usage.OutputTokens,
				}
				gotUsage = true
			}
		}
	}

	waitErr := cmd.Wait()
	if waitErr != nil && emitted.Len() == 0 {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		ch <- Token{Error: fmt.Errorf("claude: %s", errMsg)}
	}

	if !gotUsage {
		finalUsage = TokenUsage{
			InputTokens:  tokenutil.EstimateTokens(req.SystemPrompt + prompt),
			OutputTokens: tokenutil.EstimateTokens(emitted.String()),
			Estimated:    true,
		}
	}
	ch <- Token{Done: true, Usage: &finalUsage}
}

// computeDelta returns the suffix of next that is not already in already.
// Handles both cumulative (full snapshot each time) and pure-delta event
// streams. If next is not a prefix-extension of already, the entire next
// is treated as a fresh chunk.
func computeDelta(already, next string) string {
	if next == "" {
		return ""
	}
	if strings.HasPrefix(next, already) {
		return next[len(already):]
	}
	return next
}

// claudeStreamEvent is one NDJSON line from `--output-format stream-json`.
type claudeStreamEvent struct {
	Type    string                  `json:"type"`
	Message claudeStreamMessage     `json:"message"`
	Result  string                  `json:"result"`
	Usage   claudeStreamUsageFields `json:"usage"`
}

type claudeStreamMessage struct {
	Content []claudeStreamContent `json:"content"`
}

type claudeStreamContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeStreamUsageFields struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func parseClaudeStreamEvent(line []byte) (claudeStreamEvent, error) {
	var evt claudeStreamEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		return claudeStreamEvent{}, err
	}
	return evt, nil
}

func assistantText(content []claudeStreamContent) string {
	var sb strings.Builder
	for _, c := range content {
		if c.Type == "text" || c.Type == "" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// claudeJSONResult is the shape of claude --output-format json stdout.
// Kept for the non-streaming fallback parser used by tests.
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
