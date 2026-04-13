package agent

import "testing"

func TestFixInvalidGoPackage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid package unchanged",
			input:    "package main\n\nfunc main() {}\n",
			expected: "package main\n\nfunc main() {}\n",
		},
		{
			name:     "package with slash",
			input:    "package internal/controller\n\nimport \"fmt\"\n",
			expected: "package controller\n\nimport \"fmt\"\n",
		},
		{
			name:     "deeply nested slash",
			input:    "package internal/game/internal/controller\n\ntype Game struct{}\n",
			expected: "package controller\n\ntype Game struct{}\n",
		},
		{
			name:     "single segment unchanged",
			input:    "package game\n\ntype Board struct{}\n",
			expected: "package game\n\ntype Board struct{}\n",
		},
		{
			name:     "empty content",
			input:    "",
			expected: "",
		},
		{
			name:     "no package line",
			input:    "// just a comment\nfunc foo() {}\n",
			expected: "// just a comment\nfunc foo() {}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fixInvalidGoPackage(tt.input)
			if result != tt.expected {
				t.Errorf("fixInvalidGoPackage(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNormalizeBuildError(t *testing.T) {
	err1 := "internal/controller.go:1:17: expected ';', found '/'\ninternal/game.go:1:17: expected ';', found '/'\n"
	err2 := "internal/controller.go:1:17: expected ';', found '/'\ninternal/game.go:1:17: expected ';', found '/'\n"

	if normalizeBuildError(err1) != normalizeBuildError(err2) {
		t.Error("identical errors should normalize to the same string")
	}

	err3 := "internal/controller.go:5:10: undefined: foo\n"
	if normalizeBuildError(err1) == normalizeBuildError(err3) {
		t.Error("different errors should not normalize to the same string")
	}
}

func TestExtractFilesFromErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "go build errors",
			input:    "internal/game/board.go:15:3: undefined: foo\ninternal/game/state.go:8:2: imported and not used\n",
			expected: []string{"internal/game/board.go", "internal/game/state.go"},
		},
		{
			name:     "duplicates deduplicated",
			input:    "main.go:1:5: error1\nmain.go:3:2: error2\n",
			expected: []string{"main.go"},
		},
		{
			name:     "no file references",
			input:    "some random error output\n",
			expected: nil,
		},
		{
			name:     "skip hash prefixes",
			input:    "# github.com/example/project\ninternal/game.go:5:1: syntax error\n",
			expected: []string{"internal/game.go"},
		},
		{
			name:     "strip dot-slash prefix",
			input:    "./cmd/app/main.go:10:5: undefined: Run\n",
			expected: []string{"cmd/app/main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFilesFromErrors(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("extractFilesFromErrors() got %d files, want %d: %v", len(result), len(tt.expected), result)
				return
			}
			for i, got := range result {
				if got != tt.expected[i] {
					t.Errorf("file[%d] = %q, want %q", i, got, tt.expected[i])
				}
			}
		})
	}
}
