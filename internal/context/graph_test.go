package context

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGraphRepo creates a tiny module repo with two packages so that
// import/caller queries have something realistic to traverse.
func initGraphRepo(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)

	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("go.mod", "module example.com/sample\n\ngo 1.22\n")
	mustWrite("internal/foo/foo.go", `package foo

func Helper() string { return "hi" }
`)
	mustWrite("cmd/app/main.go", `package main

import "example.com/sample/internal/foo"

func main() { _ = foo.Helper() }
`)
	mustWrite("cmd/app/util.go", `package main

func uses() string { return foo() }

func foo() string { return "x" }
`)

	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	return dir
}

func TestImportsOf_LocalPackage(t *testing.T) {
	root := initGraphRepo(t)
	got, err := ImportsOf(root, "cmd/app/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "internal/foo/foo.go" {
		t.Errorf("expected [internal/foo/foo.go], got %v", got)
	}
}

func TestImportsOf_NonGo(t *testing.T) {
	root := initGraphRepo(t)
	got, err := ImportsOf(root, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for non-go, got %v", got)
	}
}

func TestCallersOf_FindsImporterNotDefinition(t *testing.T) {
	root := initGraphRepo(t)
	got, err := CallersOf(root, "internal/foo/foo.go", "Helper")
	if err != nil {
		t.Fatal(err)
	}
	// The definition file is excluded; only the importing caller qualifies.
	// cmd/app/util.go uses a local foo()/Helper-free code, so it must not match.
	if len(got) != 1 || got[0] != "cmd/app/main.go" {
		t.Errorf("expected [cmd/app/main.go], got %v", got)
	}
}

func TestCallersOf_Empty(t *testing.T) {
	root := initGraphRepo(t)
	got, err := CallersOf(root, "internal/foo/foo.go", "DefinitelyMissingSymbolXYZ")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing symbol, got %v", got)
	}
}

func TestCallersOf_IgnoresCommentAndUnimported(t *testing.T) {
	root := initGraphRepo(t)
	mustWrite := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Mentions Helper only in a comment — must not count.
	mustWrite("cmd/app/doc.go", "package main\n\n// Helper is great\nfunc note() {}\n")
	// Different package that does NOT import foo but defines its own Helper use
	// via a same-named selector on an unrelated value — must not count.
	mustWrite("internal/bar/bar.go", "package bar\n\ntype t struct{}\n\nfunc (t) Helper() {}\n\nfunc use(x t) { x.Helper() }\n")
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	got, err := CallersOf(root, "internal/foo/foo.go", "Helper")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g == "cmd/app/doc.go" {
			t.Errorf("comment-only mention must not be a caller: %v", got)
		}
		if g == "internal/bar/bar.go" {
			t.Errorf("unimported same-named symbol must not be a caller: %v", got)
		}
	}
	if len(got) != 1 || got[0] != "cmd/app/main.go" {
		t.Errorf("expected only [cmd/app/main.go], got %v", got)
	}
}

func TestPrimarySymbolOf(t *testing.T) {
	root := initGraphRepo(t)
	if got := PrimarySymbolOf(root, "internal/foo/foo.go"); got != "Helper" {
		t.Errorf("expected Helper, got %q", got)
	}
}
