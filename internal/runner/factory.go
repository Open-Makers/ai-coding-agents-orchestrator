package runner

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/skills"
)

// New creates an LLMRunner from agent config.
// skillLoader may be nil — skills will be silently skipped.
// promptLanguage controls response language (empty or "English" = no injection).
func New(cfg config.AgentConfig, skillLoader *skills.Loader, promptLanguage string) (LLMRunner, error) {
	var base LLMRunner
	switch cfg.Runner {
	case "opencode", "":
		base = OpenCodeRunner{Model: cfg.Model}

	case "ollama":
		base = NewOllamaRunner(cfg.Model)

	case "claude":
		base = ClaudeRunner{Model: cfg.Model}

	case "codex":
		base = CodexRunner{Model: cfg.Model}

	default:
		return nil, fmt.Errorf("runner: unknown runner %q (supported: opencode, claude, ollama, codex)", cfg.Runner)
	}

	if skillLoader != nil || (promptLanguage != "" && promptLanguage != "English") {
		base = &SkillRunner{inner: base, loader: skillLoader, promptLanguage: promptLanguage}
	}

	// Wrap cloud runners with token budget enforcement when configured.
	if cfg.MaxContextTokens > 0 && !IsLocalRunner(cfg) {
		base = &BudgetRunner{inner: base, maxTokens: cfg.MaxContextTokens}
	}

	return base, nil
}

// SkillRunner wraps an LLMRunner and injects skill content and language
// instructions into system prompts.
type SkillRunner struct {
	inner          LLMRunner
	loader         *skills.Loader
	promptLanguage string
}

func (r *SkillRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	req.SystemPrompt = ResolveSkills(req.SystemPrompt, req.Skills, r.loader)
	if r.promptLanguage != "" && r.promptLanguage != "English" {
		req.SystemPrompt += fmt.Sprintf("\n\nIMPORTANT: You MUST respond in %s.", r.promptLanguage)
	}
	return r.inner.Complete(ctx, req)
}

// IsLocalRunner returns true when the runner uses a local model (opencode with
// local Ollama model, or direct ollama runner). Local runners have no API cost
// so fix-attempt limits should not apply.
func IsLocalRunner(cfg config.AgentConfig) bool {
	switch cfg.Runner {
	case "ollama":
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
