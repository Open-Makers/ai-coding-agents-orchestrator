package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

	output, err := r.run(ctx, sb.String(), model)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: output}
		ch <- Token{Done: true}
		close(ch)
	}()
	return ch, nil
}

func (r OpenCodeRunner) run(ctx context.Context, prompt, model string) (string, error) {
	bin := r.Binary
	if bin == "" {
		bin = "opencode"
	}

	if model == "" {
		model = "qwen2.5-coder:3b"
	}

	args := []string{"run", "--format", "json"}
	env := os.Environ()

	// OpenCode requires "provider/model" format.
	// For local Ollama models (no provider prefix), add the ollama/ prefix.
	if !strings.Contains(model, "/") {
		model = "ollama/" + model
	}

	args = append(args, "-m", model)
	// Pass prompt as positional argument.
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: bin resolved from config, args built internally
	cmd.Env = env

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("opencode: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return extractOpenCodeResponse(out.Bytes()), nil
}

// extractOpenCodeResponse parses NDJSON event stream from opencode --format json.
// It concatenates text from all "text" type events.
func extractOpenCodeResponse(data []byte) string {
	var sb strings.Builder
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Part struct {
				Text string `json:"text"`
			} `json:"part"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type == "text" && event.Part.Text != "" {
			sb.WriteString(event.Part.Text)
		}
	}
	if sb.Len() > 0 {
		return sb.String()
	}

	// Fallback: return raw output.
	return strings.TrimSpace(string(data))
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

	cmd := exec.Command("opencode", "models")
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
