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

func TestRenderSymbolChunks_SelectsNamedAndEnclosingType(t *testing.T) {
	src := `package svc

import "fmt"

type Server struct {
	port int
}

func (s *Server) Start() error { return nil }

func (s *Server) Stop() error { return nil }

func Unrelated() { fmt.Println("x") }

func Helper() string { return "h" }
`
	out, omitted := renderSymbolChunks("svc.go", []byte(src), []string{"Start"}, 100000)
	if omitted {
		t.Error("did not expect budget omission")
	}
	// Header (package + import) is present.
	if !strings.Contains(out, "package svc") || !strings.Contains(out, `import "fmt"`) {
		t.Errorf("expected package/import header in output:\n%s", out)
	}
	// Selected method present.
	if !strings.Contains(out, "func (s *Server) Start()") {
		t.Errorf("expected Start method:\n%s", out)
	}
	// Enclosing type pulled in.
	if !strings.Contains(out, "type Server struct") {
		t.Errorf("expected enclosing Server type:\n%s", out)
	}
	// Unrelated declarations excluded.
	if strings.Contains(out, "func Unrelated()") || strings.Contains(out, "func Helper()") {
		t.Errorf("did not expect unrelated decls:\n%s", out)
	}
	if strings.Contains(out, "func (s *Server) Stop()") {
		t.Errorf("did not expect unselected Stop method:\n%s", out)
	}
}

func TestRenderSymbolChunks_TypeIncludesMethods(t *testing.T) {
	src := `package svc

type Server struct{ port int }

func (s *Server) Start() error { return nil }

func Other() {}
`
	out, _ := renderSymbolChunks("svc.go", []byte(src), []string{"Server"}, 100000)
	if !strings.Contains(out, "type Server struct") {
		t.Errorf("expected Server type:\n%s", out)
	}
	if !strings.Contains(out, "func (s *Server) Start()") {
		t.Errorf("expected Server's method when type requested:\n%s", out)
	}
	if strings.Contains(out, "func Other()") {
		t.Errorf("did not expect unrelated Other:\n%s", out)
	}
}

func TestRenderSymbolChunks_NoMatchFallsBackToWholeFile(t *testing.T) {
	src := "package svc\n\nfunc A() {}\n\nfunc B() {}\n"
	out, _ := renderSymbolChunks("svc.go", []byte(src), []string{"DoesNotExist"}, 100000)
	if !strings.Contains(out, "func A()") || !strings.Contains(out, "func B()") {
		t.Errorf("expected whole-file fallback when no symbol matches:\n%s", out)
	}
}

func TestBuildScopedSourceContext_ScopesTargetedFile(t *testing.T) {
	dir := t.TempDir()
	src := `package svc

type Server struct{ port int }

func (s *Server) Start() error { return nil }

func HugeUnrelated() string { return "` + strings.Repeat("x", 5000) + `" }
`
	if err := os.WriteFile(filepath.Join(dir, "svc.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := map[string][]string{"svc.go": {"Start"}}
	out := buildScopedSourceContext("test", dir, []string{"svc.go"}, nil, targets, 100000, 100000, 0)

	if !strings.Contains(out, "func (s *Server) Start()") {
		t.Errorf("expected targeted symbol:\n%s", out)
	}
	if strings.Contains(out, "HugeUnrelated") {
		t.Errorf("scoped render should exclude unrelated decl:\n%s", out)
	}
}

func TestRenderSymbolChunks_KeepsDocCommentWithDecl(t *testing.T) {
	src := `package svc

// Config controls retries.
type Config struct{ retries int }

// Start begins serving.
func Start() error { return nil }
`
	// Selecting only Start must carry Start's own doc comment and must NOT
	// drag in Config's doc comment via the header.
	out, _ := renderSymbolChunks("svc.go", []byte(src), []string{"Start"}, 100000)
	if !strings.Contains(out, "// Start begins serving.") {
		t.Errorf("expected Start's doc comment to travel with it:\n%s", out)
	}
	if strings.Contains(out, "Config controls retries") {
		t.Errorf("did not expect Config's doc comment to leak into header:\n%s", out)
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
