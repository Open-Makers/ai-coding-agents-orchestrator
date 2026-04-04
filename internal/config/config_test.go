package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_NoFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Language != "go" {
		t.Errorf("expected language=go, got %q", cfg.Project.Language)
	}
	if cfg.Project.TestCmd != "go test ./..." {
		t.Errorf("unexpected test_cmd: %q", cfg.Project.TestCmd)
	}
	if cfg.Agents["planner"].Runner != "claude" {
		t.Errorf("expected planner runner=claude, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_PartialFile(t *testing.T) {
	dir := t.TempDir()
	content := `
project:
  name: myapp
  test_cmd: "make test"
`
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "myapp" {
		t.Errorf("expected name=myapp, got %q", cfg.Project.Name)
	}
	if cfg.Project.TestCmd != "make test" {
		t.Errorf("expected test_cmd='make test', got %q", cfg.Project.TestCmd)
	}
	// default preserved
	if cfg.Project.Language != "go" {
		t.Errorf("expected language=go (default), got %q", cfg.Project.Language)
	}
	if cfg.Agents["reviewer"].Model != "claude-opus-4-6" {
		t.Errorf("expected reviewer model from default, got %q", cfg.Agents["reviewer"].Model)
	}
}

func TestLoad_AgentOverride(t *testing.T) {
	dir := t.TempDir()
	content := `
agents:
  coder:
    runner: codex
    model: ""
`
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agents["coder"].Runner != "codex" {
		t.Errorf("expected coder runner=codex, got %q", cfg.Agents["coder"].Runner)
	}
	// other agents still from defaults
	if cfg.Agents["planner"].Runner != "claude" {
		t.Errorf("expected planner runner=claude, got %q", cfg.Agents["planner"].Runner)
	}
}
