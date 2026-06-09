package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Language != "go" {
		t.Errorf("expected language=go, got %q", cfg.Project.Language)
	}
	if cfg.Project.TestCmd != "go test -count=1 ./..." {
		t.Errorf("unexpected test_cmd: %q", cfg.Project.TestCmd)
	}
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_LegacyFileMigration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	content := `
project:
  name: myapp
  test_cmd: "make test"
`
	// Write to legacy location (root/.orchestrator.yaml).
	legacyPath := filepath.Join(dir, Filename)
	if err := os.WriteFile(legacyPath, []byte(content), 0o644); err != nil {
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
	if cfg.Project.Language != "go" {
		t.Errorf("expected language=go (default), got %q", cfg.Project.Language)
	}

	// Legacy file should be removed after migration.
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("expected legacy file to be removed after migration")
	}

	// New file should exist.
	newPath := filepath.Join(dir, GlobalDir, ProjectFilename)
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("expected project.yaml to be created during migration")
	}
}

func TestLoad_ProjectYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Write to new location (.orchestrator/project.yaml).
	projectDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  name: newapp
  module_path: github.com/user/newapp
agents:
  coder:
    runner: claude
    model: sonnet
`
	if err := os.WriteFile(filepath.Join(projectDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "newapp" {
		t.Errorf("expected name=newapp, got %q", cfg.Project.Name)
	}
	if cfg.Project.ModulePath != "github.com/user/newapp" {
		t.Errorf("expected module_path, got %q", cfg.Project.ModulePath)
	}
	if cfg.Agents["coder"].Runner != "claude" {
		t.Errorf("expected coder runner=claude, got %q", cfg.Agents["coder"].Runner)
	}
	if cfg.Agents["coder"].Model != "sonnet" {
		t.Errorf("expected coder model=sonnet, got %q", cfg.Agents["coder"].Model)
	}
	// Non-overridden agents keep defaults.
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_AgentOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty, got %q", cfg.Agents["planner"].Runner)
	}
}

func TestLoad_GlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	globalDir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	globalContent := `
agents:
  planner:
    model: "llama3.2:latest"
  coder:
    model: "llama3.2:latest"
`
	if err := os.WriteFile(filepath.Join(globalDir, GlobalFilename), []byte(globalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project config overrides only coder model (new location).
	projectDir := t.TempDir()
	wsDir := filepath.Join(projectDir, GlobalDir)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectContent := `
agents:
  coder:
    model: "deepseek-coder-v2:latest"
`
	if err := os.WriteFile(filepath.Join(wsDir, ProjectFilename), []byte(projectContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agents["planner"].Model != "llama3.2:latest" {
		t.Errorf("expected planner model from global config, got %q", cfg.Agents["planner"].Model)
	}
	if cfg.Agents["coder"].Model != "deepseek-coder-v2:latest" {
		t.Errorf("expected coder model from project config, got %q", cfg.Agents["coder"].Model)
	}
	if cfg.Agents["planner"].Runner != "" {
		t.Errorf("expected planner runner empty (from defaults), got %q", cfg.Agents["planner"].Runner)
	}
}

func TestSave_And_LoadProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	cfg := Config{
		Project: ProjectConfig{
			Name:       "testproject",
			ModulePath: "github.com/test/project",
		},
		Agents: map[string]AgentConfig{
			"coder": {Runner: "claude", Model: "sonnet"},
		},
	}

	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file was created at new location.
	newPath := filepath.Join(dir, GlobalDir, ProjectFilename)
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Fatal("expected project.yaml to be created")
	}

	loaded := LoadProject(dir)
	if loaded.Project.Name != "testproject" {
		t.Errorf("expected name=testproject, got %q", loaded.Project.Name)
	}
	if loaded.Project.ModulePath != "github.com/test/project" {
		t.Errorf("expected module_path, got %q", loaded.Project.ModulePath)
	}
	if loaded.Agents["coder"].Runner != "claude" {
		t.Errorf("expected coder runner=claude, got %q", loaded.Agents["coder"].Runner)
	}
}

func TestSaveGlobal_CreatesDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Config{
		Agents: map[string]AgentConfig{
			"planner": {Runner: "claude", Model: "sonnet"},
		},
	}

	if err := SaveGlobal(cfg); err != nil {
		t.Fatalf("SaveGlobal failed: %v", err)
	}

	globalPath := filepath.Join(home, GlobalDir, GlobalFilename)
	if _, err := os.Stat(globalPath); os.IsNotExist(err) {
		t.Fatal("expected global config to be created")
	}

	loaded, err := loadFile(filepath.Join(home, GlobalDir), GlobalFilename)
	if err != nil {
		t.Fatalf("failed to read global config: %v", err)
	}
	if loaded.Agents["planner"].Runner != "claude" {
		t.Errorf("expected planner runner=claude, got %q", loaded.Agents["planner"].Runner)
	}
}

func TestLoadProject_NoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadProject(dir)
	if cfg.Agents != nil {
		t.Error("expected nil agents from missing project config")
	}
	if cfg.Project.Name != "" {
		t.Errorf("expected empty name, got %q", cfg.Project.Name)
	}
}

func TestLoad_ContextExcludePatternsAndSemanticIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	orchDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  context:
    exclude_patterns:
      - "*.generated.go"
      - "fixtures/**"
    semantic_index:
      enabled: true
      embedder: ollama
      model: nomic-embed-text
      top_k: 25
`
	if err := os.WriteFile(filepath.Join(orchDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Project.Context.ExcludePatterns) != 2 {
		t.Errorf("expected 2 exclude patterns, got %v", cfg.Project.Context.ExcludePatterns)
	}
	si := cfg.Project.Context.SemanticIndex
	if !si.Enabled || si.Embedder != "ollama" || si.Model != "nomic-embed-text" || si.TopK != 25 {
		t.Errorf("unexpected semantic_index: %+v", si)
	}
}

func TestLoad_ScopedContextShadow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	orchDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  context:
    scoped_context_shadow: true
`
	if err := os.WriteFile(filepath.Join(orchDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Project.Context.ScopedContextShadow {
		t.Error("expected scoped_context_shadow to be true after load")
	}
}

func TestLoad_ScopedContextCoderFix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	orchDir := filepath.Join(dir, GlobalDir)
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
project:
  context:
    scoped_context_coder_fix: true
`
	if err := os.WriteFile(filepath.Join(orchDir, ProjectFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Project.Context.ScopedContextCoderFix {
		t.Error("expected scoped_context_coder_fix to be true after load")
	}
}

func TestMerge_ModelMemory(t *testing.T) {
	dst := DefaultConfig()
	merge(&dst, Config{ModelMemory: ModelMemoryConfig{Mode: "ram", MaxRAMGB: 16}})
	if dst.ModelMemory.Mode != "ram" || dst.ModelMemory.MaxRAMGB != 16 {
		t.Fatalf("ram merge: got %+v", dst.ModelMemory)
	}

	// A later layer switching to context mode overrides the mode and value;
	// the empty-mode case must NOT clobber an existing setting.
	merge(&dst, Config{ModelMemory: ModelMemoryConfig{Mode: "context", MaxContextTokens: 8192}})
	if dst.ModelMemory.Mode != "context" || dst.ModelMemory.MaxContextTokens != 8192 {
		t.Fatalf("context merge: got %+v", dst.ModelMemory)
	}

	merge(&dst, Config{}) // empty layer
	if dst.ModelMemory.Mode != "context" {
		t.Errorf("empty layer should not clear mode, got %q", dst.ModelMemory.Mode)
	}
}
