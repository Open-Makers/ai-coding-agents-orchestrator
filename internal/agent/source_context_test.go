package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCompactSourceContext_EmptyInputs(t *testing.T) {
	result := buildCompactSourceContext("", nil, 0)
	if result != "" {
		t.Error("expected empty result for empty inputs")
	}

	result = buildCompactSourceContext("/tmp", nil, 0)
	if result != "" {
		t.Error("expected empty result for nil files")
	}

	result = buildCompactSourceContext("", []string{"a.go"}, 0)
	if result != "" {
		t.Error("expected empty result for empty root")
	}
}

func TestBuildCompactSourceContext_ReadsFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := buildCompactSourceContext(dir, []string{"main.go"}, 0)
	if !strings.Contains(result, "main.go") {
		t.Error("expected file name in output")
	}
	if !strings.Contains(result, "package main") {
		t.Error("expected file content in output")
	}
}

func TestBuildCompactSourceContext_RespectsTokenBudget(t *testing.T) {
	dir := t.TempDir()

	// Create a large file.
	largeContent := strings.Repeat("x", 10000)
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(largeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// With tight token budget (100 tokens ≈ 400 chars), output should be much smaller.
	result := buildCompactSourceContext(dir, []string{"big.go"}, 100)
	if len(result) > 1000 {
		t.Errorf("expected truncated output, got %d chars", len(result))
	}
}

func TestBuildCompactSourceContext_TruncatesLargeFiles(t *testing.T) {
	dir := t.TempDir()

	// File larger than maxReviewFileSize (6000).
	content := strings.Repeat("line\n", 2000) // 10000 chars
	if err := os.WriteFile(filepath.Join(dir, "huge.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := buildCompactSourceContext(dir, []string{"huge.go"}, 0)
	if !strings.Contains(result, "truncated") {
		t.Error("expected truncation marker for large file")
	}
}
