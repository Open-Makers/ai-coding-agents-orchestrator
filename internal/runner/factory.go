package runner

import (
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
)

// New creates an LLMRunner from agent config.
// skillLoader may be nil — skills will be silently skipped.
func New(cfg config.AgentConfig, skillLoader *skills.Loader, apiKey string) (LLMRunner, error) {
	switch cfg.Runner {
	case "claude", "anthropic":
		if apiKey == "" {
			return nil, fmt.Errorf("runner: ANTHROPIC_API_KEY required for runner=claude")
		}
		return NewAnthropicRunner(apiKey, skillLoader), nil
	case "codex", "":
		return CodexRunner{}, nil
	default:
		return nil, fmt.Errorf("runner: unknown runner %q", cfg.Runner)
	}
}
