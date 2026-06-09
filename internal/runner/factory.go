package runner

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
)

// New creates an LLMRunner from agent config.
// skillLoader may be nil — skills will be silently skipped.
// promptLanguage is accepted for backwards compatibility but ignored:
// all agents always reply in English regardless of the user's language.
func New(cfg config.AgentConfig, skillLoader *skills.Loader, promptLanguage string) (LLMRunner, error) {
	_ = promptLanguage
	var base LLMRunner
	switch cfg.Runner {
	case "opencode", "":
		base = OpenCodeRunner{Model: cfg.Model}

	case "ollama":
		base = NewOllamaRunner(cfg.Model)
		if cfg.MaxContextTokens > 0 {
			base.(*OllamaRunner).NumCtx = cfg.MaxContextTokens
		}

	case "mlx":
		base = NewMLXRunner(cfg.Model)

	case "claude":
		base = ClaudeRunner{Model: cfg.Model}

	case "codex":
		base = CodexRunner{Model: cfg.Model}

	case "copilot":
		base = CopilotRunner{Model: cfg.Model}

	case "lmstudio":
		base = NewLMStudioRunner(cfg.Model)

	default:
		return nil, fmt.Errorf("runner: unknown runner %q (supported: opencode, claude, ollama, mlx, codex, copilot, lmstudio)", cfg.Runner)
	}

	if skillLoader != nil {
		base = &SkillRunner{inner: base, loader: skillLoader}
	}

	// Wrap with token budget enforcement when configured. For cloud runners
	// this caps cost; for local runners it trims the prompt to the bounded
	// context window so an over-long prompt is not silently truncated (or
	// rejected) by the model after it loads.
	if cfg.MaxContextTokens > 0 {
		base = &BudgetRunner{inner: base, maxTokens: cfg.MaxContextTokens}
	}

	return base, nil
}

// SkillRunner wraps an LLMRunner and injects skill content into system
// prompts. Agent responses are always English; the user-language directive
// has been removed to keep agent output uniform across locales.
type SkillRunner struct {
	inner  LLMRunner
	loader *skills.Loader
}

func (r *SkillRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	req.SystemPrompt = ResolveSkills(req.SystemPrompt, req.Skills, r.loader)
	return r.inner.Complete(ctx, req)
}

// IsLocalRunner returns true when the runner uses a local model (opencode with
// local Ollama model, or direct ollama runner). Local runners have no API cost
// so fix-attempt limits should not apply.
func IsLocalRunner(cfg config.AgentConfig) bool {
	switch cfg.Runner {
	case "ollama":
		return true
	case "mlx":
		return true
	case "lmstudio":
		return true
	case "opencode", "":
		return cfg.Model == "" || !contains(cfg.Model, "/")
	default:
		return false
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
