package orchestrator

import (
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
