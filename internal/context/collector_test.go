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
