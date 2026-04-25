package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
)

func createProjectMarkers(root string) error {
	return os.MkdirAll(filepath.Join(root, ".git"), 0o755)
}

func TestStartupHome_NewTaskStartsPMChat(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { _ = RemoveRecentProject(root) })
	_ = createProjectMarkers(root)
	cfg := newTestConfig()
	cfg.Project.Language = "python"
	wsDir := filepath.Join(root, artifacts.DirName)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir ws: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, artifacts.RequirementsFile), []byte("old reqs"), 0o644); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, artifacts.VisionFile), []byte("vision"), 0o644); err != nil {
		t.Fatalf("write vision: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, artifacts.ProjectConfigFile), []byte("project: {}\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	m := newStartupModel(root, filepath.Join(root, ".orchestrator", "requirements.md"), cfg)

	updated, cmd := m.Update(homeSelectedMsg{action: homeActionNewTask})
	if cmd == nil {
		t.Fatal("expected quit command for PM chat flow")
	}

	startup, ok := updated.(startupModel)
	if !ok {
		t.Fatal("expected startupModel")
	}
	if !startup.chatMode {
		t.Fatal("expected New Task to enable PM chat mode")
	}
	if _, err := os.Stat(filepath.Join(wsDir, artifacts.RequirementsFile)); !os.IsNotExist(err) {
		t.Fatal("expected New Task to remove requirements.md")
	}
	if _, err := os.Stat(filepath.Join(wsDir, artifacts.VisionFile)); !os.IsNotExist(err) {
		t.Fatal("expected New Task to remove generated artifacts")
	}
	if _, err := os.Stat(filepath.Join(wsDir, artifacts.ProjectConfigFile)); err != nil {
		t.Fatalf("expected project config to be preserved: %v", err)
	}
}

func TestStartupHome_RunPipelineOpensPicker(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { _ = RemoveRecentProject(root) })
	_ = createProjectMarkers(root)
	cfg := newTestConfig()
	cfg.Project.Language = "python"

	m := newStartupModel(root, filepath.Join(root, ".orchestrator", "requirements.md"), cfg)

	updated, cmd := m.Update(homeSelectedMsg{action: homeActionRunPipeline})

	startup, ok := updated.(startupModel)
	if !ok {
		t.Fatal("expected startupModel")
	}
	if startup.phase != startupPhasePicker {
		t.Fatalf("expected picker phase, got %v", startup.phase)
	}
	if cmd != nil {
		_ = cmd()
	}
}

func TestStartupPicker_EscReturnsToHome(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { _ = RemoveRecentProject(root) })
	_ = createProjectMarkers(root)
	cfg := newTestConfig()
	cfg.Project.Language = "python"

	m := newStartupModel(root, filepath.Join(root, ".orchestrator", "requirements.md"), cfg)
	updated, _ := m.Update(homeSelectedMsg{action: homeActionRunPipeline})

	startup, ok := updated.(startupModel)
	if !ok {
		t.Fatal("expected startupModel")
	}
	if startup.phase != startupPhasePicker {
		t.Fatalf("expected picker phase, got %v", startup.phase)
	}

	updated, cmd := startup.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected command returning to home")
	}

	startup, ok = updated.(startupModel)
	if !ok {
		t.Fatal("expected startupModel")
	}
	msg := cmd()
	updated, _ = startup.Update(msg)
	startup, ok = updated.(startupModel)
	if !ok {
		t.Fatal("expected startupModel")
	}
	if startup.phase != startupPhaseHome {
		t.Fatalf("expected home phase after Esc, got %v", startup.phase)
	}
}
