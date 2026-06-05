package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCompactSourceContext_EmptyInputs(t *testing.T) {
	result := buildCompactSourceContext("test", "", nil, 0)
	if result != "" {
		t.Error("expected empty result for empty inputs")
	}

	result = buildCompactSourceContext("test", "/tmp", nil, 0)
	if result != "" {
		t.Error("expected empty result for nil files")
	}

	result = buildCompactSourceContext("test", "", []string{"a.go"}, 0)
	if result != "" {
		t.Error("expected empty result for empty root")
	}
}

func TestBuildCompactSourceContext_ReadsFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := buildCompactSourceContext("test", dir, []string{"main.go"}, 0)
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
	result := buildCompactSourceContext("test", dir, []string{"big.go"}, 100)
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

	result := buildCompactSourceContext("test", dir, []string{"huge.go"}, 0)
	if !strings.Contains(result, "truncated") {
		t.Error("expected truncation marker for large file")
	}
}

func TestBuildCompactSourceContext_SeedExpandsImports(t *testing.T) {
	dir := t.TempDir()
	cmds := [][]string{
		{"init"}, {"config", "user.email", "t@t.com"}, {"config", "user.name", "t"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module example.com/s\n\ngo 1.22\n")
	mustWrite("internal/foo/foo.go", "package foo\n\nfunc Helper() string { return \"hi\" }\n")
	mustWrite("cmd/app/main.go", "package main\n\nimport \"example.com/s/internal/foo\"\n\nfunc main() { _ = foo.Helper() }\n")
	mustWrite("unrelated/x.go", "package unrelated\n\nfunc X() {}\n")

	out := buildCompactSourceContext("test", dir, []string{"unrelated/x.go"}, 0, "cmd/app/main.go")

	mainIdx := strings.Index(out, "cmd/app/main.go")
	fooIdx := strings.Index(out, "internal/foo/foo.go")
	unrIdx := strings.Index(out, "unrelated/x.go")
	if mainIdx < 0 || fooIdx < 0 || unrIdx < 0 {
		t.Fatalf("missing one of the expected paths in output:\n%s", out)
	}
	if mainIdx >= fooIdx || fooIdx >= unrIdx {
		t.Errorf("expected order seed → import → unrelated, got main=%d foo=%d unr=%d", mainIdx, fooIdx, unrIdx)
	}
}

func TestExpandWithGraph_Reasons(t *testing.T) {
	dir := t.TempDir()

	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module example.com/s\n\ngo 1.22\n")
	mustWrite("internal/foo/foo.go", "package foo\n\nfunc Helper() string { return \"hi\" }\n")
	mustWrite("cmd/app/main.go", "package main\n\nimport \"example.com/s/internal/foo\"\n\nfunc main() { _ = foo.Helper() }\n")
	mustWrite("unrelated/x.go", "package unrelated\n\nfunc X() {}\n")

	_, reasons := expandWithGraph(dir, []string{"unrelated/x.go"}, []string{"cmd/app/main.go"})

	if got := reasons["cmd/app/main.go"]; got != reasonSeed {
		t.Errorf("seed reason = %q, want %q", got, reasonSeed)
	}
	if got := reasons["internal/foo/foo.go"]; got != reasonImport {
		t.Errorf("import reason = %q, want %q", got, reasonImport)
	}
	if got := reasons["unrelated/x.go"]; got != reasonFile {
		t.Errorf("caller-supplied reason = %q, want %q", got, reasonFile)
	}
}

func TestExpandWithGraph_NoSeeds_AllFiles(t *testing.T) {
	ordered, reasons := expandWithGraph("/tmp", []string{"a.go", "b.go", "a.go"}, nil)
	if len(ordered) != 2 {
		t.Fatalf("expected deduped 2 files, got %v", ordered)
	}
	for _, f := range ordered {
		if reasons[f] != reasonFile {
			t.Errorf("reason[%s] = %q, want %q", f, reasons[f], reasonFile)
		}
	}
}

func TestBuildCompactSourceContext_ChunkBoundaries(t *testing.T) {
	dir := t.TempDir()

	// Build a Go file that exceeds maxReviewFileSize so chunking kicks in.
	var src strings.Builder
	src.WriteString("package big\n\n")
	for i := 0; i < 200; i++ {
		_, _ = fmt.Fprintf(&src, "func F%d() string { return \"%s\" }\n\n", i, strings.Repeat("x", 40))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out := buildCompactSourceContext("test", dir, []string{"big.go"}, 0)

	// Output braces must balance — chunking never splits a function mid-body.
	open := strings.Count(out, "{")
	closeC := strings.Count(out, "}")
	if open != closeC {
		t.Errorf("unbalanced braces: { = %d, } = %d", open, closeC)
	}
}
