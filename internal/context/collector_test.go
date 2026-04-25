package context

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// initGitRepo creates a minimal git repo so that Collect() does not fail early
// on the `git ls-files` call.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "t@t.com"},
		{"config", "user.name", "t"},
	}
	for _, args := range cmds {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCollect_AlwaysInclude_PathTraversal(t *testing.T) {
	root := initGitRepo(t)

	// Write a "secret" file outside the root.
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("sensitive"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Attempt a relative path that would escape root (e.g., "../../secret.txt").
	rel, _ := filepath.Rel(root, secret)

	cfg := config.Config{}
	cfg.Project.Context.AlwaysInclude = []string{rel, "../other-file.txt"}

	pc, _ := Collect(root, cfg)

	for k := range pc.AlwaysInclude {
		t.Errorf("expected no AlwaysInclude entries, got key %q", k)
	}
}

func TestCollect_AlwaysInclude_ValidRelativePath(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{}
	cfg.Project.Context.AlwaysInclude = []string{"notes.md"}

	pc, _ := Collect(root, cfg)

	if _, ok := pc.AlwaysInclude["notes.md"]; !ok {
		t.Error("expected notes.md to be included")
	}
}

func TestCollect_AlwaysInclude_AbsolutePathRejected(t *testing.T) {
	root := initGitRepo(t)

	cfg := config.Config{}
	cfg.Project.Context.AlwaysInclude = []string{"/etc/passwd"}

	pc, _ := Collect(root, cfg)

	if _, ok := pc.AlwaysInclude["/etc/passwd"]; ok {
		t.Error("absolute path should have been rejected")
	}
}

func TestFilterInternalPaths_DropsOrchestratorAndVCS(t *testing.T) {
	in := []string{
		"main.go",
		".orchestrator/project.yaml",
		".orchestrator/prompts/coder.md",
		"internal/foo/foo.go",
		".git/HEAD",
		"vendor/lib/lib.go",
		"node_modules/pkg/index.js",
	}
	got := filterInternalPaths(in)
	want := []string{"main.go", "internal/foo/foo.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCollect_ExcludesOrchestratorWorkspace(t *testing.T) {
	root := initGitRepo(t)

	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "project.yaml"), []byte("agents: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addAll := exec.Command("git", "add", "-A")
	addAll.Dir = root
	if out, err := addAll.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	pc, err := Collect(root, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range pc.Files {
		if filepath.ToSlash(f) == ".orchestrator/project.yaml" {
			t.Fatalf("expected .orchestrator/project.yaml to be excluded, got files: %v", pc.Files)
		}
	}
}
