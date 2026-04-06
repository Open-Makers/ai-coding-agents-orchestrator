package context

import (
	"testing"
)

func TestDetectBrownfield(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		expected bool
	}{
		{
			name:     "empty project is greenfield",
			files:    nil,
			expected: false,
		},
		{
			name:     "single go file is greenfield",
			files:    []string{"main.go"},
			expected: false,
		},
		{
			name:     "two source files is brownfield",
			files:    []string{"cmd/app/main.go", "internal/core/game.go"},
			expected: true,
		},
		{
			name:     "non-source files only is greenfield",
			files:    []string{"README.md", "go.mod", "go.sum", ".gitignore"},
			expected: false,
		},
		{
			name:     "mixed files with enough source is brownfield",
			files:    []string{"README.md", "cmd/app/main.go", "internal/core/game.go", "go.mod"},
			expected: true,
		},
		{
			name:     "vendor files excluded",
			files:    []string{"vendor/pkg/a.go", "vendor/pkg/b.go"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectBrownfield(tt.files, "go")
			if result != tt.expected {
				t.Errorf("detectBrownfield(%v) = %v, want %v", tt.files, result, tt.expected)
			}
		})
	}
}

func TestIsSourceFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"cmd/app/main.go", true},
		{"internal/core/game.go", true},
		{"internal/core/game_test.go", true},
		{"src/main.rs", true},
		{"app.py", true},
		{"src/App.tsx", true},
		{"README.md", false},
		{"go.mod", false},
		{".gitignore", false},
		{"vendor/pkg/dep.go", false},
		{"node_modules/pkg/index.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isSourceFile(tt.path)
			if result != tt.expected {
				t.Errorf("isSourceFile(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestRankSourceFiles(t *testing.T) {
	files := []string{
		"internal/core/game_test.go",
		"README.md",
		"go.mod",
		"internal/core/game.go",
		"cmd/app/main.go",
		"internal/cli/cli.go",
		"config/config.go",
	}

	ranked := rankSourceFiles(files)

	if len(ranked) == 0 {
		t.Fatal("expected ranked files, got none")
	}

	// Entry point should be first.
	if ranked[0] != "cmd/app/main.go" {
		t.Errorf("expected cmd/app/main.go first, got %q", ranked[0])
	}

	// Test files should be near the end.
	lastSource := ranked[len(ranked)-1]
	if lastSource != "internal/core/game_test.go" {
		t.Errorf("expected test file last, got %q", lastSource)
	}

	// Non-source files should be excluded.
	for _, f := range ranked {
		if f == "README.md" || f == "go.mod" {
			t.Errorf("non-source file %q should not be in ranked list", f)
		}
	}
}

func TestBuildTreeStructure(t *testing.T) {
	files := []string{
		"cmd/app/main.go",
		"internal/core/game.go",
		"internal/cli/cli.go",
		"config/config.go",
	}

	tree := buildTreeStructure(files)
	if tree == "" {
		t.Fatal("expected non-empty tree")
	}

	// Should contain directory names.
	for _, dir := range []string{"cmd/", "internal/", "core/", "cli/", "config/"} {
		if !contains(tree, dir) {
			t.Errorf("tree should contain %q, got:\n%s", dir, tree)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchIn(s, substr)
}

func searchIn(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
