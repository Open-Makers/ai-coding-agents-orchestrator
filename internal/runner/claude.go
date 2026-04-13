package runner

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
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
// The Claude CLI does not provide a non-interactive model listing command,
// so we return the static list of supported aliases.
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

	output, err := r.run(userContent.String(), req.SystemPrompt)
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

func (r ClaudeRunner) run(prompt, systemPrompt string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "claude"
	}

	args := []string{"--print", "--no-session-persistence", "--allowedTools", ""}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	// Prompt is piped via stdin to avoid macOS ARG_MAX limits on large prompts.

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
