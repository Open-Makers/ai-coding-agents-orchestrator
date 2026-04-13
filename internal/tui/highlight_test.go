package tui

import (
	"strings"
	"testing"
)

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"go", "Go"},
		{"golang", "Go"},
		{"python", "Python"},
		{"py", "Python"},
		{"javascript", "JavaScript"},
		{"js", "JavaScript"},
		{"typescript", "TypeScript"},
		{"ts", "TypeScript"},
		{"rust", "Rust"},
		{"rs", "Rust"},
		{"bash", "Bash"},
		{"sh", "Bash"},
		{"", ""},
		{"unknown_lang", "unknown_lang"},
	}
	for _, tt := range tests {
		got := normalizeLanguage(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractFenceLang(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"```go", "Go"},
		{"```python", "Python"},
		{"```go cmd/main.go", "Go"},
		{"```ts:src/app.ts", "TypeScript"},
		{"```", ""},
		{"~~~rust", "Rust"},
	}
	for _, tt := range tests {
		got := extractFenceLang(tt.input)
		if got != tt.expected {
			t.Errorf("extractFenceLang(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHighlighterHighlightLines(t *testing.T) {
	h := newHighlighter("go")

	lines := []string{
		"wrote: cmd/app/main.go",
		"```go",
		"package main",
		"",
		"import \"fmt\"",
		"",
		"func main() {",
		"\tfmt.Println(\"hello\")",
		"}",
		"```",
		"build: OK",
	}

	result := h.highlightLines(lines)

	// Fence lines and non-code lines should be preserved.
	if result[0] != "wrote: cmd/app/main.go" {
		t.Errorf("non-code line should be preserved, got %q", result[0])
	}
	if result[len(result)-1] != "build: OK" {
		t.Errorf("trailing non-code line should be preserved, got %q", result[len(result)-1])
	}

	// Code between fences should contain ANSI escape sequences.
	codeSection := strings.Join(result[2:len(result)-2], "\n")
	if !strings.Contains(codeSection, "\x1b[") {
		t.Error("highlighted code should contain ANSI escape sequences")
	}
}

func TestHighlighterNilSafe(t *testing.T) {
	var h *highlighter
	lines := []string{"hello", "world"}
	result := h.highlightLines(lines)
	if len(result) != 2 || result[0] != "hello" {
		t.Error("nil highlighter should return lines unchanged")
	}
}

func TestHighlighterEmptyLanguage(t *testing.T) {
	h := newHighlighter("")
	lines := []string{"```go", "package main", "```"}
	result := h.highlightLines(lines)
	// With empty language, no highlighting should occur.
	if len(result) != len(lines) {
		t.Errorf("empty language highlighter should return %d lines, got %d", len(lines), len(result))
	}
}

func TestHighlightCode(t *testing.T) {
	code := `func main() {
	fmt.Println("hello")
}`
	result := highlightCode(code, "Go", nil)
	if !strings.Contains(result, "\x1b[") {
		t.Error("highlightCode should produce ANSI output for Go code")
	}
	if !strings.Contains(result, "func") {
		t.Error("highlighted output should contain the original keywords")
	}
}

func TestIsFenceOpenClose(t *testing.T) {
	if !isFenceOpen("```go") {
		t.Error("```go should be a fence open")
	}
	if !isFenceOpen("~~~python") {
		t.Error("~~~python should be a fence open")
	}
	if isFenceOpen("hello ```") {
		t.Error("text before ``` should not be a fence open")
	}
	if !isFenceClose("```") {
		t.Error("``` should be a fence close")
	}
	if isFenceClose("```go") {
		t.Error("```go should NOT be a fence close")
	}
}
