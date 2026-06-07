package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestBuildResumeContext_IncludesDiffAndStatus(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@t.com")
	git("config", "user.name", "t")

	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "init")
	// Uncommitted modification → must appear in the resume diff.
	if err := os.WriteFile(file, []byte("package main\n\nfunc main() { println(\"WIP_MARKER\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := artifacts.Workspace{Root: root, Dir: filepath.Join(root, artifacts.DirName)}
	if err := os.MkdirAll(ws.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	tr := NewTaskRunner(bus.New(), nil, config.Config{}, ws, root)

	rc := tr.buildResumeContext(context.Background())

	if !strings.Contains(rc, "Resuming an interrupted task") {
		t.Errorf("missing resume header:\n%s", rc)
	}
	if !strings.Contains(rc, "```diff") || !strings.Contains(rc, "WIP_MARKER") {
		t.Errorf("expected uncommitted diff with WIP_MARKER:\n%s", rc)
	}
	// No QA agent configured → build/tests can't pass → reported as FAILING.
	if !strings.Contains(rc, "FAILING") {
		t.Errorf("expected build/test status line:\n%s", rc)
	}
}
