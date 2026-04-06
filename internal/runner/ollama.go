package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const ollamaDefaultModel = "qwen2.5-coder:latest"

// OllamaRunner executes Ollama models through the Claude Code CLI.
// Uses: ollama launch claude --model <model> --yes -- --print --allowedTools ""
type OllamaRunner struct {
	Model string
}

func NewOllamaRunner(model string) *OllamaRunner {
	if model == "" {
		model = ollamaDefaultModel
	}
	return &OllamaRunner{Model: model}
}

func (r *OllamaRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = r.Model
	}

	// Merge system prompt and user messages into a single prompt.
	// Small Ollama models generate JSON tool-calls when --system-prompt
	// is passed separately, so everything goes into -p with an explicit
	// plain-text instruction.
	var prompt strings.Builder
	if req.SystemPrompt != "" {
		prompt.WriteString(req.SystemPrompt)
		prompt.WriteString("\n\nIMPORTANT: Output plain text only, not JSON.\n\n")
	}
	for _, m := range req.Messages {
		prompt.WriteString(strings.ToUpper(m.Role))
		prompt.WriteString(":\n")
		prompt.WriteString(m.Content)
		prompt.WriteString("\n\n")
	}

	output, err := r.run(prompt.String(), model)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: string(output)}
		ch <- Token{Done: true}
		close(ch)
	}()
	return ch, nil
}

func (r *OllamaRunner) run(prompt, model string) ([]byte, error) {
	// ollama launch claude handles env vars (ANTHROPIC_BASE_URL etc.)
	// --allowedTools with a space disables all tools so the model produces
	// plain text (empty string "" is silently dropped by ollama launch).
	args := []string{
		"launch", "claude",
		"--model", model, "--yes",
		"--",
		"--print", "--allowedTools", " ",
		"-p", prompt,
	}

	cmd := exec.Command("ollama", args...) //nolint:gosec // #nosec G204 -- args built internally for ollama CLI
	cmd.Env = os.Environ()
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ollama (via claude): %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
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
