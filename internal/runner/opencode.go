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

// OpenCodeRunner executes the OpenCode CLI in non-interactive mode as an LLM backend.
// Uses `opencode run` with --format json for structured output.
type OpenCodeRunner struct {
	Binary string
	Model  string
}

// Complete implements LLMRunner by running the OpenCode CLI with `opencode run`.
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

func (r OpenCodeRunner) runRaw(ctx context.Context, prompt, model string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "opencode"
	}

	if model == "" {
		model = "qwen2.5-coder:3b"
	}

	args := []string{"run", "--format", "json"}

	// OpenCode requires "provider/model" format.
	// For local Ollama models (no provider prefix), add the ollama/ prefix.
	if !strings.Contains(model, "/") {
		model = "ollama/" + model
	}

	args = append(args, "-m", model)
	// Prompt is piped via stdin to avoid macOS ARG_MAX limits on large prompts.
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

// extractOpenCodeResponse is kept for backward compatibility.
func extractOpenCodeResponse(data []byte) string {
	text, _ := extractOpenCodeResponseWithUsage(data)
	return text
}

// OpenCodeAvailableModels is a fallback list used when the CLI is unavailable.
var OpenCodeAvailableModels = []string{
	"qwen2.5-coder:3b",
	"qwen2.5-coder:latest",
	"llama3.2:latest",
	"deepseek-coder-v2:latest",
	"opencode/big-pickle",
	"opencode/gpt-5-nano",
}

// OpenCodeListModels returns available models from the OpenCode CLI.
func OpenCodeListModels() ([]string, error) {
	var models []string

	cmd := executil.Command("opencode", "models")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err == nil {
		for _, line := range strings.Split(out.String(), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				models = append(models, line)
			}
		}
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no models found (is opencode installed?)")
	}
	return models, nil
}
