package context

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCollect_DiffExcludesOrchestrator(t *testing.T) {
	root := initGitRepo(t)

	// Initial commit so HEAD exists.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "init")

	// Modify both a real source file and an .orchestrator/ file.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".orchestrator", "project.yaml"), []byte("agents:\n  coder:\n    runner: claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")

	pc, err := Collect(root, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if pc.UnstagedDiff == "" {
		t.Fatal("expected non-empty diff")
	}
	if strings.Contains(pc.UnstagedDiff, ".orchestrator/project.yaml") {
		t.Fatalf("diff must not include .orchestrator/project.yaml, got:\n%s", pc.UnstagedDiff)
	}
	if !strings.Contains(pc.UnstagedDiff, "main.go") {
		t.Fatalf("expected main.go change in diff, got:\n%s", pc.UnstagedDiff)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestFilterInternalPaths_DropsExtendedDirsAndNoise(t *testing.T) {
	in := []string{
		"src/app.go",
		"dist/main.js",
		"build/output.o",
		"target/debug/foo",
		"out/bin",
		".next/cache/x",
		".nuxt/dist/x",
		".cache/y",
		".parcel-cache/z",
		"app/__pycache__/m.pyc",
		".venv/lib/python3/site-packages/x.py",
		"venv/lib/x",
		".tox/py3/x",
		"coverage/lcov.info",
		".coverage/data",
		"tmp/scratch",
		".idea/workspace.xml",
		".vscode/settings.json",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"Cargo.lock",
		"Gemfile.lock",
		"poetry.lock",
		"web/jquery.min.js",
		"web/style.min.css",
		"web/bundle.js.map",
		"go.sum",
	}
	got := filterInternalPaths(in)
	for _, f := range got {
		if f != "src/app.go" && f != "go.sum" {
			t.Errorf("unexpected path retained: %q", f)
		}
	}
	if len(got) != 2 {
		t.Errorf("expected only src/app.go and go.sum to survive, got: %v", got)
	}
}

func TestGitArgsWithExcludes_IncludesExtendedDirs(t *testing.T) {
	args := gitArgsWithExcludes("ls-files")
	joined := strings.Join(args, " ")
	for _, dir := range []string{"dist", "build", "target", ".next", "__pycache__", "coverage", ".idea"} {
		if !strings.Contains(joined, dir+"/**") {
			t.Errorf("expected %s to be excluded in git args, got %q", dir, joined)
		}
	}
}

func TestIsNoiseFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"package-lock.json", true},
		{"a/b/yarn.lock", true},
		{"app/jquery.min.js", true},
		{"styles/main.min.css", true},
		{"bundle.js.map", true},
		{"go.sum", false},
		{"src/main.go", false},
		{"README.md", false},
	}
	for _, c := range cases {
		if got := isNoiseFile(c.path); got != c.want {
			t.Errorf("isNoiseFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestFilterInternalPaths_ExcludePatterns(t *testing.T) {
	in := []string{"src/api.gen.go", "src/api.go", "tools/foo.generated.go"}
	got := filterInternalPaths(in, "*.gen.go", "*.generated.go")
	if len(got) != 1 || got[0] != "src/api.go" {
		t.Errorf("expected only src/api.go, got %v", got)
	}
}
