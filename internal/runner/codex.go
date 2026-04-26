package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// CodexRunner executes the OpenAI Codex CLI as an LLM backend.
// Uses `codex --quiet` for non-interactive text output.
type CodexRunner struct {
	Binary string
	Model  string
}

// CodexModels lists known Codex-compatible model names.
var CodexModels = []string{
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",
}

// CodexListModels returns available models by merging the currently configured
// model from ~/.codex/config.toml with the known models list.
// No API keys or network calls required.
func CodexListModels() ([]string, error) {
	configured := readCodexConfigModel()
	if configured == "" {
		return CodexModels, nil
	}

	// Put configured model first, then append others (skip duplicates).
	models := []string{configured}
	for _, m := range CodexModels {
		if m != configured {
			models = append(models, m)
		}
	}
	return models, nil
}

// readCodexConfigModel reads the top-level "model" value from ~/.codex/config.toml.
func readCodexConfigModel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	codexDir := filepath.Join(home, ".codex")
	data, err := safefile.ReadFile(codexDir, "config.toml")
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Match top-level: model = "value" (stop at first section header).
		if strings.HasPrefix(line, "[") {
			break
		}
		if strings.HasPrefix(line, "model") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, `"'`)
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

// Complete implements LLMRunner by running the Codex CLI with the given prompt.
// Stdout is streamed incrementally so the TUI shows progress live instead of
// a single burst at the end of generation.
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

	cmd, stdout, stderr, err := r.startStreamingProcess(ctx, userContent.String(), req.SystemPrompt, model)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 16)
	fullInput := req.SystemPrompt + "\n\n" + userContent.String()
	go streamCodexOutput(cmd, stdout, stderr, model, fullInput, ch)
	return ch, nil
}

func (r CodexRunner) startStreamingProcess(ctx context.Context, prompt, systemPrompt, model string) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	bin := r.Binary
	if bin == "" {
		bin = "codex"
	}

	// Merge system prompt and user prompt into one block for stdin piping.
	fullPrompt := prompt
	if systemPrompt != "" {
		fullPrompt = systemPrompt + "\n\n" + prompt
	}

	args := []string{"exec", "--full-auto"}
	if model != "" {
		args = append(args, "--model", model)
	}
	// Prompt is piped via stdin to avoid macOS ARG_MAX limits on large prompts.

	cmd := executil.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(fullPrompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("codex: start: %w", err)
	}
	return cmd, stdout, stderr, nil
}

// streamCodexOutput forwards stdout chunks as Token deltas while the CLI runs,
// then emits a final Done token with token usage computed from the full output.
func streamCodexOutput(cmd *exec.Cmd, stdout io.ReadCloser, stderr *bytes.Buffer, model, fullInput string, ch chan<- Token) {
	defer close(ch)

	var collected strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			collected.WriteString(chunk)
			ch <- Token{Text: chunk}
		}
		if readErr != nil {
			break
		}
	}

	waitErr := cmd.Wait()
	output := collected.String()
	if waitErr != nil && strings.TrimSpace(output) == "" {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		if rl := ClassifyRateLimit("codex", errMsg, output); rl != nil {
			ch <- Token{Error: rl}
		} else {
			ch <- Token{Error: fmt.Errorf("codex: %w: %s", waitErr, errMsg)}
		}
		return
	}

	usage := countCodexTokens(model, fullInput, output)
	ch <- Token{Done: true, Usage: &usage}
}

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
		// cl100k_base fallback — counts are approximate for this model.
		inputToks := enc.Encode(inputText, nil, nil)
		outputToks := enc.Encode(outputText, nil, nil)
		return TokenUsage{
			InputTokens:  len(inputToks),
			OutputTokens: len(outputToks),
			Estimated:    true,
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
