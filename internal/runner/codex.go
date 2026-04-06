package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	f, err := os.Open(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: string(output)}
		ch <- Token{Done: true}
		close(ch)
	}()
	return ch, nil
}

func (r CodexRunner) run(ctx context.Context, prompt, systemPrompt, model string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "codex"
	}

	args := []string{"exec", "--full-auto"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if systemPrompt != "" {
		args = append(args, "--", systemPrompt+"\n\n"+prompt)
	} else {
		args = append(args, "--", prompt)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
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
