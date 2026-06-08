package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
)

func TestHasReviewableArtifacts(t *testing.T) {
	root := t.TempDir()
	if HasReviewableArtifacts(root) {
		t.Fatal("expected no reviewable artifacts in an empty project")
	}

	ws, _ := artifacts.EnsureWorkspace(root)
	if err := ws.WriteFile(artifacts.SummaryFile, []byte("done so far")); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if !HasReviewableArtifacts(root) {
		t.Error("expected reviewable artifacts once an artifact exists")
	}
}

func TestHasReviewableArtifacts_IgnoresBlank(t *testing.T) {
	root := t.TempDir()
	ws, _ := artifacts.EnsureWorkspace(root)
	if err := ws.WriteFile(artifacts.RequirementsFile, []byte("   \n")); err != nil {
		t.Fatal(err)
	}
	if HasReviewableArtifacts(root) {
		t.Error("blank artifact should not count as reviewable")
	}
}

func TestBuildProjectReviewSeed(t *testing.T) {
	root := t.TempDir()
	if seed := BuildProjectReviewSeed(root); seed != "" {
		t.Fatalf("expected empty seed with no artifacts, got %q", seed)
	}

	ws, _ := artifacts.EnsureWorkspace(root)
	if err := ws.WriteFile(artifacts.RequirementsFile, []byte("Build a tic-tac-toe game")); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFile(artifacts.SubTasksFile, []byte(`{"tasks":[{"key":"T1"}]}`)); err != nil {
		t.Fatal(err)
	}

	seed := BuildProjectReviewSeed(root)
	if seed == "" {
		t.Fatal("expected a non-empty seed when artifacts exist")
	}
	for _, want := range []string{
		"Resume this project",
		"remaining work",
		artifacts.RequirementsFile,
		"Build a tic-tac-toe game",
		artifacts.SubTasksFile,
	} {
		if !strings.Contains(seed, want) {
			t.Errorf("seed missing %q:\n%s", want, seed)
		}
	}
}

func TestHasReviewableArtifacts_DetectsMemoryOnly(t *testing.T) {
	// A project whose only artifacts are persisted task memory (the common case
	// after a run finishes/fails) must still be reviewable.
	root := t.TempDir()
	ws, _ := artifacts.EnsureWorkspace(root)
	memTasks := filepath.Join(ws.Dir, artifacts.MemoryDirName, "tasks")
	if err := os.MkdirAll(memTasks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memTasks, "20260608-1144-task.md"), []byte("PM decided X"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !HasReviewableArtifacts(root) {
		t.Error("expected memory-only project to be reviewable")
	}
	seed := BuildProjectReviewSeed(root)
	if !strings.Contains(seed, "PM decided X") {
		t.Errorf("expected memory content in seed:\n%s", seed)
	}
}
