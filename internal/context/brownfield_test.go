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

	ranked := rankSourceFiles("", files, nil)

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

func TestSystemPromptFragment_ProfileCompact(t *testing.T) {
	pc := ProjectContext{
		Files:         []string{"main.go", "internal/pkg/pkg.go"},
		RecentCommits: []string{"abc1234 initial commit"},
		UnstagedDiff:  "some diff",
		IsBrownfield:  true,
		ProjectType:   "go",
		TreeStructure: "cmd/\ninternal/\n  pkg/",
		SourceFiles:   map[string]string{"main.go": "package main"},
	}

	compact := pc.SystemPromptFragment(ProfileCompact)
	full := pc.SystemPromptFragment(ProfileFull)

	// Compact should be significantly shorter.
	if len(compact) >= len(full) {
		t.Errorf("compact (%d) should be shorter than full (%d)", len(compact), len(full))
	}

	// Compact should include project type and tree.
	if !contains(compact, "go") {
		t.Error("compact should include project type")
	}
	if !contains(compact, "cmd/") {
		t.Error("compact should include tree structure")
	}

	// Compact should NOT include commits, diffs, source code, or file listings.
	if contains(compact, "Recent Commits") {
		t.Error("compact should not include commits")
	}
	if contains(compact, "Uncommitted Changes") {
		t.Error("compact should not include diffs")
	}
	if contains(compact, "Existing Source Code") {
		t.Error("compact should not include source files")
	}
	if contains(compact, "Files (") {
		t.Error("compact should not include file listing")
	}

	// Full should include everything.
	if !contains(full, "Recent Commits") {
		t.Error("full should include commits")
	}
	if !contains(full, "Existing Source Code") {
		t.Error("full should include source files")
	}
}

func TestSystemPromptFragment_DefaultIsFull(t *testing.T) {
	pc := ProjectContext{
		Files:       []string{"main.go"},
		ProjectType: "go",
	}

	// No profile argument should default to full.
	defaultResult := pc.SystemPromptFragment()
	fullResult := pc.SystemPromptFragment(ProfileFull)

	if defaultResult != fullResult {
		t.Error("default profile should match ProfileFull")
	}
}
