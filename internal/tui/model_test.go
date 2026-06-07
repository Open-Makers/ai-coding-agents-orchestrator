package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

func TestModel_PMConversationSeedsTaskInput(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.orchestrator", nil, config.Config{}).
		WithTaskInput("# Build tic-tac-toe\nImplement a CLI game")

	// First PM conversation message opens the negotiate overlay.
	msg := BusMessageMsg{Msg: bus.NewMessage(bus.RolePM, "", bus.MsgConversation,
		bus.ConversationPayload{From: "pm", Content: "What grid size?"})}
	updated, _ := m.Update(msg)
	got := updated.(Model)

	if got.overlay != overlayNegotiate {
		t.Fatalf("expected negotiate overlay to open, got %v", got.overlay)
	}
	view := got.overlayNegotiate.vp.View()
	if !strings.Contains(view, "Build tic-tac-toe") {
		t.Errorf("expected submitted task input seeded in PM conversation:\n%s", view)
	}
	if !strings.Contains(view, "What grid size?") {
		t.Errorf("expected PM message shown:\n%s", view)
	}
}

func TestModel_TabEntersTreeBrowsingAndEscExits(t *testing.T) {
	root := setupTreeRoot(t)
	m := New(nil, root, filepath.Join(root, ".orchestrator"), nil, config.Config{})
	m.width, m.height = 160, 40 // wide enough for sysmon
	m.layout()
	m.sysmon.SetProjectRoot(root)

	if !m.canBrowseTree() {
		t.Fatal("expected tree browsing to be available at width 160")
	}

	// Tab enters browsing.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if !got.treeBrowsing || !got.sysmon.TreeFocused() {
		t.Fatalf("Tab should enter tree browsing: browsing=%v focused=%v", got.treeBrowsing, got.sysmon.TreeFocused())
	}

	// Arrow keys move the selection without leaving browse mode.
	got, _ = mustUpdate(t, got, tea.KeyMsg{Type: tea.KeyDown})
	if !got.treeBrowsing {
		t.Fatal("down arrow should stay in browse mode")
	}

	// Esc exits browsing.
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.treeBrowsing || got.sysmon.TreeFocused() {
		t.Fatalf("Esc should exit tree browsing: browsing=%v focused=%v", got.treeBrowsing, got.sysmon.TreeFocused())
	}
}

func mustUpdate(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

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

// During the build-fix loop the coder emits a "coder_fixer" state. The status
// bar must surface the coder_fixer model, not the coder model.
func TestConfigKeyForState_CoderFixer(t *testing.T) {
	if got := configKeyForState("coder_fixer"); got != "coder_fixer" {
		t.Errorf("coder_fixer state should map to coder_fixer key, got %q", got)
	}
	if got := configKeyForState("coder"); got != "coder" {
		t.Errorf("coder state should map to coder key, got %q", got)
	}
	if got := configKeyForState("qa_tests"); got != "qa" {
		t.Errorf("qa_tests state should map to qa role key, got %q", got)
	}
}

func TestRunnerModelForState_CoderFixerDistinctModel(t *testing.T) {
	agents := map[string]config.AgentConfig{
		"pm":          {Runner: "mlx", Model: "gemma"},
		"qa":          {Runner: "mlx", Model: "gemma"},
		"coder":       {Runner: "mlx", Model: "gemma"},
		"coder_fixer": {Runner: "lmstudio", Model: "qwen-coder"},
	}

	r, mdl := runnerModelForKey(agents, configKeyForState("coder_fixer"))
	if r != "lmstudio" || mdl != "qwen-coder" {
		t.Errorf("expected lmstudio/qwen-coder for coder_fixer, got %s/%s", r, mdl)
	}

	// Plain coder state must still show the coder model.
	r, mdl = runnerModelForKey(agents, configKeyForState("coder"))
	if r != "mlx" || mdl != "gemma" {
		t.Errorf("expected mlx/gemma for coder, got %s/%s", r, mdl)
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

func TestStatusBar_WrapsWhenTooNarrow(t *testing.T) {
	// Wide terminal: everything on one line.
	wide := NewStatusBar(220).WithBranch("master").WithState("coder").
		WithRunnerModel("lmstudio", "google/gemma-4-12b-qat")
	if wide.Height() != 1 {
		t.Errorf("wide bar should be 1 line, got %d", wide.Height())
	}

	// Narrow terminal: shortcuts wrap to additional lines instead of overflowing.
	narrow := NewStatusBar(60).WithBranch("master").WithState("coder").
		WithRunnerModel("lmstudio", "google/gemma-4-12b-qat")
	if narrow.Height() < 2 {
		t.Errorf("narrow bar should wrap to >=2 lines, got %d", narrow.Height())
	}
	// No rendered line may exceed the terminal width.
	for i, line := range strings.Split(narrow.View(), "\n") {
		if w := lipglossLen(line); w > 60 {
			t.Errorf("line %d width %d exceeds terminal width 60: %q", i, w, line)
		}
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

func TestModel_CtrlPDuringPipelineOpensPauseConfirm(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if cmd != nil {
		t.Fatal("expected no quit command until pause is confirmed")
	}
	model := updated.(Model)
	if !model.pauseConfirm {
		t.Fatal("expected Ctrl+P to open pause confirmation")
	}
	if model.pauseForModel {
		t.Fatal("should not flag pause before confirmation")
	}
}

func TestModel_PauseConfirmQuitsWithFlag(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})
	m.pauseConfirm = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command after confirming pause")
	}
	model := updated.(Model)
	if !model.PauseForModelChange() {
		t.Fatal("expected PauseForModelChange to be true after confirm")
	}
}

func TestModel_CtrlPIgnoredAfterPipelineDone(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})
	m.pipelineDone = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if updated.(Model).pauseConfirm {
		t.Fatal("Ctrl+P should be ignored once the pipeline is done")
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
