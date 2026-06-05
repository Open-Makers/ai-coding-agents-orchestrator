package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

func TestRunnerModelForRole_ExplicitConfig(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":    {Runner: "codex", Model: "gpt-5.4"},
		"qa":    {Runner: "codex", Model: "gpt-5.4"},
		"coder": {Runner: "claude", Model: "sonnet"},

		"security": {Runner: "claude", Model: "opus"},
	}

	tests := []struct {
		role           bus.AgentRole
		expectedRunner string
		expectedModel  string
	}{
		{bus.RoleCoder, "claude", "sonnet"},
		{bus.RoleQA, "codex", "gpt-5.4"},

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
		"pm":    {Runner: "codex", Model: "gpt-5"},
		"coder": {Runner: "codex", Model: "gpt-5"},
	}

	// "qa" not in config — should fall back to default (codex/gpt-5).
	r, mdl := runnerModelForRole(agents, "qa")
	if r != "codex" || mdl != "gpt-5" {
		t.Errorf("expected codex/gpt-5 fallback, got %s/%s", r, mdl)
	}
}

func TestRunnerModelForRole_PartialConfig_RunnerOnly(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":    {Runner: "codex", Model: "gpt-5"},
		"qa":    {Runner: "codex", Model: "gpt-5"},
		"coder": {Runner: "claude", Model: ""},
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
		"pm":    {Runner: "codex", Model: "gpt-5"},
		"qa":    {Runner: "codex", Model: "gpt-5"},
		"coder": {Runner: "", Model: "sonnet"},
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
		"pm":    {Runner: "codex", Model: "gpt-5"},
		"qa":    {Runner: "codex", Model: "gpt-5"},
		"coder": {Runner: "", Model: ""},
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
			"qa": {Runner: "claude", Model: "opus"},
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
			"qa": {Runner: "", Model: "gpt-5"},
		},
	}

	r, _ := runnerModelFromConfig(cfg)
	if r != "opencode" {
		t.Errorf("expected default runner 'opencode', got %q", r)
	}
}

func TestRunnerModelFromConfig_NoQA(t *testing.T) {
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
	sb := NewStatusBar(120).WithState("coder").WithStageInfo("Stage 2/5: Must Have — Auth")
	view := sb.View()
	if view == "" {
		t.Fatal("status bar view should not be empty")
	}
}

func TestModel_EscDuringPipelineOpensCancelConfirm(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected no quit command while pipeline is still running")
	}

	model, ok := updated.(Model)
	if !ok {
		t.Fatal("expected tui.Model")
	}
	if !model.cancelConfirm {
		t.Fatal("expected esc to open cancel confirmation")
	}
	if model.returnToMenu {
		t.Fatal("should not return to menu before cancellation is confirmed")
	}
}

func TestModel_EscAfterPipelineReturnsToMenu(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})
	m.pipelineDone = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected quit command after completed pipeline")
	}

	model, ok := updated.(Model)
	if !ok {
		t.Fatal("expected tui.Model")
	}
	if !model.returnToMenu {
		t.Fatal("expected esc to return to menu after pipeline completion")
	}
}

func TestModel_CtrlEIsNoopWithoutRunner(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{})
	m.pipelineDone = true
	m.pipelineFailed = true
	m.pipelineErr = "build fix stuck"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	got := updated.(Model)
	if got.pipelineResuming {
		t.Fatal("expected resume to be skipped when no runner is attached")
	}
	if !got.pipelineFailed {
		t.Fatal("expected pipelineFailed to remain true when there's nothing to resume")
	}
}

func TestModel_NegotiateEscReturnsToMenuInTaskFlow(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{})
	m.overlay = overlayNegotiate
	m.taskRunner = orchestrator.NewTaskRunner(nil, nil, config.Config{}, artifacts.Workspace{}, "")

	cancelled := false
	m.cancelFunc = func() { cancelled = true }

	updated, cmd := m.Update(NegotiateClosedMsg{})
	if cmd == nil {
		t.Fatal("expected quit command when closing negotiate overlay in task flow")
	}

	got := updated.(Model)
	if got.overlay != overlayNone {
		t.Fatal("expected negotiate overlay to close")
	}
	if !got.returnToMenu {
		t.Fatal("expected task flow escape to return to menu")
	}
	if !cancelled {
		t.Fatal("expected task flow escape to cancel the running task")
	}
}

func TestModel_NegotiateEscReturnsToMenuBeforePMStarts(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{})
	m.overlay = overlayNegotiate
	m.phase = "negotiating"
	m.overlayNegotiate = NewNegotiate(nil)

	cancelled := false
	m.cancelFunc = func() { cancelled = true }

	updated, cmd := m.Update(NegotiateClosedMsg{})
	if cmd == nil {
		t.Fatal("expected quit command when closing empty negotiate overlay at startup")
	}

	got := updated.(Model)
	if got.overlay != overlayNone {
		t.Fatal("expected negotiate overlay to close")
	}
	if !got.returnToMenu {
		t.Fatal("expected escape to return to menu before PM starts")
	}
	if !cancelled {
		t.Fatal("expected escape to cancel pending run before PM starts")
	}
}

func TestModel_NegotiateEscReturnsToMenuDuringPipelineNegotiation(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{})
	m.overlay = overlayNegotiate
	m.phase = "negotiating"
	m.overlayNegotiate = NewNegotiate(nil)
	m.overlayNegotiate.lines = []chatLine{{role: "assistant", content: "Which files should change?"}}

	cancelled := false
	m.cancelFunc = func() { cancelled = true }

	updated, cmd := m.Update(NegotiateClosedMsg{})
	if cmd == nil {
		t.Fatal("expected quit command when closing negotiate overlay during negotiation")
	}

	got := updated.(Model)
	if got.overlay != overlayNone {
		t.Fatal("expected negotiate overlay to close")
	}
	if !got.returnToMenu {
		t.Fatal("expected escape to return to menu during pipeline negotiation")
	}
	if !cancelled {
		t.Fatal("expected escape to cancel active negotiation")
	}
}

func TestModel_CtrlAApprovesTaskRunnerGate(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{})
	tr := orchestrator.NewTaskRunner(nil, nil, config.Config{}, artifacts.Workspace{}, "")
	m.taskRunner = tr
	m.gateArtifact = "task_spec.json"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got := updated.(Model)

	if got.gateArtifact != "" {
		t.Fatal("expected gate artifact to be cleared after approval")
	}
	if !got.approvedGates["task_spec.json"] {
		t.Fatal("expected task_spec.json to be marked approved")
	}
}

func TestModel_ArtifactViewerApproveUsesTaskRunner(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{})
	tr := orchestrator.NewTaskRunner(nil, nil, config.Config{}, artifacts.Workspace{}, "")
	m.taskRunner = tr
	m.overlay = overlayArtifact
	m.gateArtifact = "task_spec.json"

	updated, cmd := m.Update(artifactViewerClosedMsg{approved: true})
	if cmd == nil {
		t.Fatal("expected waitForBusEvent command after closing artifact viewer")
	}
	got := updated.(Model)

	if got.overlay != overlayNone {
		t.Fatal("expected artifact viewer to close")
	}
	if got.gateArtifact != "" {
		t.Fatal("expected gate artifact to be cleared after viewer approval")
	}
	if !got.approvedGates["task_spec.json"] {
		t.Fatal("expected task_spec.json to be marked approved")
	}
}
