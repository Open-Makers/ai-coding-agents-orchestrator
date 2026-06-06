package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

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

func TestCoderBuildSourceContextWithSeeds_ExpandsImports(t *testing.T) {
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
	mustWrite("internal/bar/bar.go", "package bar\n\nimport \"example.com/s/internal/foo\"\n\nfunc Use() string { return foo.Helper() }\n")
	mustWrite("unrelated/x.go", "package unrelated\n\nfunc X() {}\n")

	a := &CoderAgent{root: dir}

	// Seed = file referenced in error output. Importers and imports of
	// the seed must end up in the rendered context, before unrelated files.
	out := a.buildSourceContextWithSeeds(
		[]string{"unrelated/x.go"},
		[]string{"internal/bar/bar.go"},
	)

	barIdx := strings.Index(out, "internal/bar/bar.go")
	fooIdx := strings.Index(out, "internal/foo/foo.go")
	unrIdx := strings.Index(out, "unrelated/x.go")
	if barIdx < 0 || fooIdx < 0 || unrIdx < 0 {
		t.Fatalf("missing one of the expected paths in output:\n%s", out)
	}
	if barIdx >= fooIdx || fooIdx >= unrIdx {
		t.Errorf("expected order seed → import → unrelated, got bar=%d foo=%d unr=%d",
			barIdx, fooIdx, unrIdx)
	}
}

func TestWriteOneFile_NoOpRewriteSkipped(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "x.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "package x\n\nfunc Foo() {}\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &CoderAgent{root: root}
	err := a.writeOneFile("pkg/x.go", original)
	if err != errNoOpRewrite {
		t.Fatalf("expected errNoOpRewrite for identical content, got %v", err)
	}

	// Modified content must still be written.
	if err := a.writeOneFile("pkg/x.go", "package x\n\nfunc Foo() { _ = 1 }\n"); err != nil {
		t.Fatalf("unexpected error on real change: %v", err)
	}
	out, _ := os.ReadFile(target)
	if !strings.Contains(string(out), "_ = 1") {
		t.Errorf("expected real change persisted, got %q", string(out))
	}
}

// TestStreamAndWriteFiles_NoOpCountsAsRecognised guards against a regression
// where a coder run that reformatted files into proper file blocks (but did
// not actually change byte content) was treated as "no file blocks found",
// triggering a wasted retry that produced the same identical output and then
// aborted the pipeline with `coder: no file blocks found in initial code
// generation output`.
//
// A no-op rewrite must still count as a recognised file block so the empty-
// output retry path does not fire.
func TestStreamAndWriteFiles_NoOpCountsAsRecognised(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "x.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "package x\n\nfunc Foo() {}\n"
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &CoderAgent{root: root, BaseAgent: NewBase("coder", bus.New())}

	ch := make(chan runner.Token, 16)
	for _, line := range []string{
		"**pkg/x.go**\n",
		"```go\n",
		"package x\n",
		"\n",
		"func Foo() {}\n",
		"```\n",
	} {
		ch <- runner.Token{Text: line}
	}
	ch <- runner.Token{Done: true}
	close(ch)

	written, _, _, err := a.streamAndWriteFiles(ch)
	if err != nil {
		t.Fatalf("streamAndWriteFiles: %v", err)
	}
	if len(written) != 1 || written[0] != "pkg/x.go" {
		t.Errorf("expected pkg/x.go recognised as no-op write, got written=%v", written)
	}
}

func TestBuildScopedRelatedContext_AdditiveAndNonDestructive(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("changed.go", "package p\n\nfunc Changed() {}\n")
	write("related.go", "package p\n\nfunc Related() {}\n\nfunc HugeUnrelated() string { return \""+strings.Repeat("x", 4000)+"\" }\n")

	a := &CoderAgent{root: dir, BaseAgent: NewBase("coder", bus.New())}

	// No targets → no related context.
	if got := a.buildScopedRelatedContext([]string{"changed.go"}, nil, nil); got != "" {
		t.Errorf("expected empty related context without targets, got %q", got)
	}

	// Target on a mandatory (changed) file → excluded (never re-add the diff).
	if got := a.buildScopedRelatedContext([]string{"changed.go"}, nil, map[string][]string{"changed.go": {"Changed"}}); got != "" {
		t.Errorf("expected mandatory file excluded from related context, got %q", got)
	}

	// Target on a new related file → scoped render added (only the named decl).
	got := a.buildScopedRelatedContext([]string{"changed.go"}, nil, map[string][]string{"related.go": {"Related"}})
	if !strings.Contains(got, "func Related()") {
		t.Errorf("expected Related() in related context:\n%s", got)
	}
	if strings.Contains(got, "HugeUnrelated") {
		t.Errorf("related context should be scoped, not whole-file:\n%s", got)
	}
}
