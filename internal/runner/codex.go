package runner

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// CodexRunner executes the Codex CLI as an LLM backend.
type CodexRunner struct {
	Binary string
	Env    []string
}

// Complete implements LLMRunner by building a single prompt and running it through Codex CLI.
func (r CodexRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	if len(req.Skills) > 0 {
		log.Printf("codex: skills ignored (%v)", req.Skills)
	}

	var sb strings.Builder
	if req.SystemPrompt != "" {
		sb.WriteString("SYSTEM:\n")
		sb.WriteString(req.SystemPrompt)
		sb.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(":\n")
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}

	output, err := r.run(sb.String())
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

func (r CodexRunner) run(prompt string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "codex"
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), r.Env...)
	cmd.Stdin = strings.NewReader(prompt)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codex: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}
