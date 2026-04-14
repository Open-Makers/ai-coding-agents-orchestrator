package runner

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

func (r CodexRunner) run(ctx context.Context, prompt, systemPrompt, model string) ([]byte, error) {
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
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(out.String())
		}
		return nil, fmt.Errorf("codex: %w: %s", err, errMsg)
	}
	return out.Bytes(), nil
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
