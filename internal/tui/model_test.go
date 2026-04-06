package tui

import (
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestRunnerModelForRole_ExplicitConfig(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":       {Runner: "codex", Model: "gpt-5.4"},
		"planner":  {Runner: "codex", Model: "gpt-5.4"},
		"coder":    {Runner: "claude", Model: "sonnet"},
		"tester":   {Runner: "codex", Model: "gpt-5.3-codex"},
		"security": {Runner: "claude", Model: "opus"},
	}

	tests := []struct {
		role           bus.AgentRole
		expectedRunner string
		expectedModel  string
	}{
		{bus.RoleCoder, "claude", "sonnet"},
		{bus.RolePlanner, "codex", "gpt-5.4"},
		{"tester", "codex", "gpt-5.3-codex"},
		{"security", "claude", "opus"},
		{bus.RolePM, "codex", "gpt-5.4"},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			r, mdl := runnerModelForRole(agents, tt.role)
			if r != tt.expectedRunner {
				t.Errorf("role %s: expected runner %q, got %q", tt.role, tt.expectedRunner, r)
			}
			if mdl != tt.expectedModel {
				t.Errorf("role %s: expected model %q, got %q", tt.role, tt.expectedModel, mdl)
			}
		})
	}
}

func TestRunnerModelForRole_FallsBackToDefault(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":      {Runner: "codex", Model: "gpt-5"},
		"planner": {Runner: "codex", Model: "gpt-5"},
		"coder":   {Runner: "codex", Model: "gpt-5"},
	}

	// "reviewer" not in config — should fall back to default (codex/gpt-5).
	r, mdl := runnerModelForRole(agents, "reviewer")
	if r != "codex" || mdl != "gpt-5" {
		t.Errorf("expected codex/gpt-5 fallback, got %s/%s", r, mdl)
	}
}

func TestRunnerModelForRole_PartialConfig_RunnerOnly(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":      {Runner: "codex", Model: "gpt-5"},
		"planner": {Runner: "codex", Model: "gpt-5"},
		"coder":   {Runner: "claude", Model: ""},
	}

	r, mdl := runnerModelForRole(agents, "coder")
	if r != "claude" {
		t.Errorf("expected runner 'claude', got %q", r)
	}
	if mdl != "gpt-5" {
		t.Errorf("expected model fallback 'gpt-5', got %q", mdl)
	}
}

func TestRunnerModelForRole_PartialConfig_ModelOnly(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":      {Runner: "codex", Model: "gpt-5"},
		"planner": {Runner: "codex", Model: "gpt-5"},
		"coder":   {Runner: "", Model: "sonnet"},
	}

	r, mdl := runnerModelForRole(agents, "coder")
	if mdl != "sonnet" {
		t.Errorf("expected model 'sonnet', got %q", mdl)
	}
	if r != "codex" {
		t.Errorf("expected runner fallback 'codex', got %q", r)
	}
}

func TestRunnerModelForRole_EmptyAgentConfig(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":      {Runner: "codex", Model: "gpt-5"},
		"planner": {Runner: "codex", Model: "gpt-5"},
		"coder":   {Runner: "", Model: ""},
	}

	// Both empty — should use global default.
	r, mdl := runnerModelForRole(agents, "coder")
	if r != "codex" || mdl != "gpt-5" {
		t.Errorf("expected codex/gpt-5 fallback, got %s/%s", r, mdl)
	}
}

func TestRunnerModelFromConfig(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"planner": {Runner: "claude", Model: "opus"},
		},
	}

	r, mdl := runnerModelFromConfig(cfg)
	if r != "claude" || mdl != "opus" {
		t.Errorf("expected claude/opus, got %s/%s", r, mdl)
	}
}

func TestRunnerModelFromConfig_DefaultRunner(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"planner": {Runner: "", Model: "gpt-5"},
		},
	}

	r, _ := runnerModelFromConfig(cfg)
	if r != "opencode" {
		t.Errorf("expected default runner 'opencode', got %q", r)
	}
}

func TestRunnerModelFromConfig_NoPlanner(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"coder": {Runner: "claude", Model: "sonnet"},
		},
	}

	r, mdl := runnerModelFromConfig(cfg)
	if r != "opencode" || mdl != "" {
		t.Errorf("expected opencode/'', got %s/%s", r, mdl)
	}
}

func TestExtractStageInfo(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"── Stage 2/5: Must Have — Auth ──", "Stage 2/5: Must Have — Auth"},
		{"── Stage 1/3: Core setup ──", "Stage 1/3: Core setup"},
		{"Stage 4/4: Should Have — Stats", "Stage 4/4: Should Have — Stats"},
		{"coding", ""},
		{"all tests passed", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractStageInfo(tt.input)
			if result != tt.expected {
				t.Errorf("extractStageInfo(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestStatusBar_StageInfo(t *testing.T) {
	sb := NewStatusBar(120).WithState("coding").WithStageInfo("Stage 2/5: Must Have — Auth")
	view := sb.View()
	if view == "" {
		t.Fatal("status bar view should not be empty")
	}
}
