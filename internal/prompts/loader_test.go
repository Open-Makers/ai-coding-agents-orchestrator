package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportPrompt(t *testing.T) {
	destDir := t.TempDir()

	path, err := ExportPrompt("pm-system", destDir)
	if err != nil {
		t.Fatalf("ExportPrompt: %v", err)
	}

	if filepath.Base(path) != "pm-system.md" {
		t.Errorf("expected pm-system.md, got %s", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}

	if len(data) == 0 {
		t.Error("exported file is empty")
	}

	if !contains(string(data), "Product Manager") {
		t.Error("exported file should contain PM system prompt content")
	}
}

func TestExportPrompt_NotFound(t *testing.T) {
	destDir := t.TempDir()

	_, err := ExportPrompt("nonexistent-prompt", destDir)
	if err == nil {
		t.Error("expected error for nonexistent prompt")
	}
}

func TestOverrideExists(t *testing.T) {
	dir := t.TempDir()

	if OverrideExists("pm-system", dir) {
		t.Error("should not exist before export")
	}

	_, _ = ExportPrompt("pm-system", dir)

	if !OverrideExists("pm-system", dir) {
		t.Error("should exist after export")
	}
}

func TestOverrideExists_EmptyDir(t *testing.T) {
	if OverrideExists("pm-system", "") {
		t.Error("should return false for empty dir")
	}
}

func TestPromptsForRole(t *testing.T) {
	tests := []struct {
		role     string
		minCount int
	}{
		{"pm", 1},
		{"qa", 2},
		{"coder", 3},

		{"ux_reviewer", 1},
		{"security", 1},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			names := PromptsForRole(tt.role)
			if len(names) < tt.minCount {
				t.Errorf("PromptsForRole(%q) returned %d prompts, want at least %d", tt.role, len(names), tt.minCount)
			}
		})
	}
}

func TestPromptsForRole_Unknown(t *testing.T) {
	names := PromptsForRole("nonexistent")
	if names != nil {
		t.Errorf("expected nil for unknown role, got %v", names)
	}
}

func TestLoaderOverride(t *testing.T) {
	dir := t.TempDir()

	customContent := "You are a custom PM agent. %s"
	if err := os.WriteFile(filepath.Join(dir, "pm-system.md"), []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := New()
	loader.override = dir

	content, err := loader.Load("pm-system")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if content != customContent {
		t.Errorf("expected custom content, got %q", content)
	}
}

func TestLoaderFallbackToEmbedded(t *testing.T) {
	dir := t.TempDir() // empty dir — no overrides

	loader := New()
	loader.override = dir

	content, err := loader.Load("pm-system")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !contains(content, "Product Manager") {
		t.Error("should fall back to embedded prompt")
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
