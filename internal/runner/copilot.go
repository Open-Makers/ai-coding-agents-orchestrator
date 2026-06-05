package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// CopilotRunner executes the GitHub Copilot CLI (`copilot`) as an LLM backend.
// Uses `copilot -p <prompt>` for non-interactive prompt mode with `--allow-all-tools`
// so the CLI does not pause on tool-permission prompts.
type CopilotRunner struct {
	Binary string
	Model  string
}

// copilotListModelArgs are the subcommands probed when discovering models.
// As of CLI 1.0.x there is no such subcommand — the `/model` picker is
// interactive-only — but we keep the probes for forward compatibility with
// future releases that may add a `--list-models` or similar flag.
var copilotListModelArgs = [][]string{
	{"--list-models"},
	{"models"},
	{"model", "list"},
}

// CopilotModels is the curated fallback list of model IDs accepted by
// `copilot --model`. Mirrors the entries shown by the interactive `/model`
// picker. Kept in sync manually; the CLI does not expose a discovery command.
var CopilotModels = []string{
	"claude-sonnet-4.6",
	"claude-sonnet-4.5",
	"claude-haiku-4.5",
	"claude-opus-4.8",
	"claude-opus-4.7",
	"claude-opus-4.6",
	"claude-opus-4.5",
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5-mini",
	"gpt-4.1",
}

// CopilotListModels returns the models the local Copilot CLI accepts. It
// prefers CLI-reported models when a future release exposes them, otherwise
// falls back to CopilotModels with the user's currently configured model
// (from ~/.copilot/settings.json) moved to the front.
func CopilotListModels() ([]string, error) {
	bin := "copilot"
	if _, err := exec.LookPath(bin); err != nil {
		return nil, fmt.Errorf("copilot CLI not found in PATH")
	}

	if models := probeCopilotCLIModels(bin); len(models) > 0 {
		return models, nil
	}

	return copilotFallbackModels(readCopilotConfiguredModel()), nil
}

// probeCopilotCLIModels tries known discovery subcommands. Returns nil if none
// produce parseable output.
func probeCopilotCLIModels(bin string) []string {
	for _, args := range copilotListModelArgs {
		out, err := executil.Command(bin, args...).Output()
		if err != nil {
			continue
		}
		if models := parseCopilotModelsOutput(out); len(models) > 0 {
			return models
		}
	}
	return nil
}

// copilotFallbackModels returns CopilotModels with `configured` moved to the
// front (and added if missing). Empty `configured` returns CopilotModels as-is.
func copilotFallbackModels(configured string) []string {
	if configured == "" {
		return CopilotModels
	}
	models := []string{configured}
	for _, m := range CopilotModels {
		if m != configured {
			models = append(models, m)
		}
	}
	return models
}

// readCopilotConfiguredModel reads the "model" field from ~/.copilot/settings.json.
// Returns "" on any error — this is a best-effort hint, not a hard requirement.
func readCopilotConfiguredModel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".copilot", "settings.json"))
	if err != nil {
		return ""
	}
	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Model)
}

// parseCopilotModelsOutput extracts model identifiers from arbitrary CLI output.
// Accepts simple "id" lists, two-column "name id" tables (returns the id column),
// and ignores headers, blanks, and obvious decorations.
func parseCopilotModelsOutput(out []byte) []string {
	seen := make(map[string]bool)
	var models []string
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Skip table headers.
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "DISPLAY NAME") || strings.HasPrefix(upper, "MODEL") || strings.HasPrefix(upper, "ID ") {
			continue
		}
		// Take the last whitespace-separated token — for "Display Name  vendor/id"
		// this yields the id; for plain "vendor/id" it yields the line itself.
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[len(fields)-1]
		if !looksLikeCopilotModelID(id) || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	return models
}

// looksLikeCopilotModelID returns true for tokens that plausibly identify a
// model — alphanumeric with dashes, dots, colons, or vendor "/" prefixes.
// Must contain at least one letter to avoid matching bare version numbers.
func looksLikeCopilotModelID(s string) bool {
	if len(s) < 2 || len(s) > 80 {
		return false
	}
	hasLetter := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			hasLetter = true
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
		case r == '-' || r == '.' || r == '_' || r == '/' || r == ':':
		default:
			return false
		}
	}
	return hasLetter
}

// Complete implements LLMRunner by running the Copilot CLI with the given prompt.
// Stdout is streamed incrementally so the TUI shows progress live.
func (r CopilotRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
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
	go streamCopilotOutput(cmd, stdout, stderr, fullInput, ch)
	return ch, nil
}

func (r CopilotRunner) startStreamingProcess(ctx context.Context, prompt, systemPrompt, model string) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	bin := r.Binary
	if bin == "" {
		bin = "copilot"
	}

	// Merge system prompt and user prompt into one block; the Copilot CLI has
	// no dedicated --system flag, so we prepend the system instructions inline.
	fullPrompt := prompt
	if systemPrompt != "" {
		fullPrompt = systemPrompt + "\n\n" + prompt
	}

	args := []string{"-p", fullPrompt, "--allow-all-tools", "--no-color"}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := executil.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("copilot: stdout pipe: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("copilot: start: %w", err)
	}
	return cmd, stdout, stderr, nil
}

// streamCopilotOutput forwards stdout chunks as Token deltas while the CLI runs,
// then emits a final Done token with token usage estimated from the I/O text.
// The Copilot CLI does not report token counts on stdout, so usage is always
// estimated heuristically.
func streamCopilotOutput(cmd *exec.Cmd, stdout io.ReadCloser, stderr *bytes.Buffer, fullInput string, ch chan<- Token) {
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
		if rl := ClassifyRateLimit("copilot", errMsg, output); rl != nil {
			ch <- Token{Error: rl}
		} else {
			ch <- Token{Error: fmt.Errorf("copilot: %w: %s", waitErr, errMsg)}
		}
		return
	}

	usage := TokenUsage{
		InputTokens:  tokenutil.EstimateTokens(fullInput),
		OutputTokens: tokenutil.EstimateTokens(output),
		Estimated:    true,
	}
	ch <- Token{Done: true, Usage: &usage}
}
