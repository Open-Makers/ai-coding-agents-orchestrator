package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestApplyProjectSetup_PersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := setupDoneMsg{
		agentOverrides: map[string]agentSetupOverride{
			"coder": {runner: "lmstudio", model: "qwen-coder"},
		},
	}

	cfg, ok := applyProjectSetup(root, msg)
	if !ok {
		t.Fatal("applyProjectSetup should succeed")
	}
	ac := cfg.Agents["coder"]
	if ac.Runner != "lmstudio" || ac.Model != "qwen-coder" {
		t.Fatalf("expected lmstudio/qwen-coder, got %s/%s", ac.Runner, ac.Model)
	}

	// The override must be persisted to the project config file.
	proj := config.LoadProject(root)
	if proj.Agents["coder"].Model != "qwen-coder" {
		t.Fatalf("override not persisted, got %q", proj.Agents["coder"].Model)
	}
}
