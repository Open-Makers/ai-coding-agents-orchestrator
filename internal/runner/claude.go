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

// Complete implements LLMRunner by running the Claude CLI with the given prompt.
func (r ClaudeRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	var userContent strings.Builder
	for _, m := range req.Messages {
		userContent.WriteString(strings.ToUpper(m.Role))
		userContent.WriteString(":\n")
		userContent.WriteString(m.Content)
		userContent.WriteString("\n\n")
	}

	raw, err := r.run(userContent.String(), req.SystemPrompt)
	if err != nil {
		return nil, err
	}

	text, usage, _ := parseClaudeJSONResponse(raw)
	// If usage is estimated, improve estimate using input content length.
	if usage.Estimated {
		usage.InputTokens = tokenutil.EstimateTokens(req.SystemPrompt + userContent.String())
	}

	ch := make(chan Token, 2)
	go func() {
		ch <- Token{Text: text}
		ch <- Token{Done: true, Usage: &usage}
		close(ch)
	}()
	return ch, nil
}

func (r ClaudeRunner) run(prompt, systemPrompt string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "claude"
	}

	// --output-format json gives structured output including usage stats.
	args := []string{"--output-format", "json", "--no-session-persistence", "--allowedTools", ""}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

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

// claudeJSONResult is the shape of claude --output-format json stdout.
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
