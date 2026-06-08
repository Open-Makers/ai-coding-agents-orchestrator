package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
)

func TestRunStateRoundTrip(t *testing.T) {
	ws := artifacts.Workspace{Root: t.TempDir(), Dir: t.TempDir()}
	tr := &TaskRunner{ws: ws, taskBeadID: "bead-123", runID: "run-abc"}

	if _, ok := tr.readRunState(); ok {
		t.Fatal("expected no run state before writing")
	}

	if err := tr.writeRunState("Build a thing"); err != nil {
		t.Fatalf("writeRunState: %v", err)
	}

	st, ok := tr.readRunState()
	if !ok {
		t.Fatal("expected run state after writing")
	}
	if st.TopBeadID != "bead-123" || st.RunID != "run-abc" || st.Title != "Build a thing" {
		t.Errorf("unexpected run state: %+v", st)
	}
	if st.SchemaVersion != runStateSchemaVersion {
		t.Errorf("schema version: want %d, got %d", runStateSchemaVersion, st.SchemaVersion)
	}
	if st.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
}

func TestSubTasksRoundTrip(t *testing.T) {
	ws := artifacts.Workspace{Root: t.TempDir(), Dir: t.TempDir()}
	tr := &TaskRunner{ws: ws}

	if _, ok := tr.readSubTasks(); ok {
		t.Fatal("expected no sub-task plan before writing")
	}

	tasks := []agent.SubTask{
		{Key: "T1", Title: "First", Description: "do first"},
		{Key: "T2", Title: "Second", Description: "do second", DependsOn: []string{"T1"}},
	}
	if err := tr.writeSubTasks(tasks, map[string]string{"T1": "bead-1", "T2": "bead-2"}); err != nil {
		t.Fatalf("writeSubTasks: %v", err)
	}
	plan, ok := tr.readSubTasks()
	if !ok {
		t.Fatal("expected sub-task plan after writing")
	}
	if len(plan.Tasks) != 2 || plan.IDByKey["T1"] != "bead-1" || plan.Tasks[1].DependsOn[0] != "T1" {
		t.Errorf("unexpected plan: %+v", plan)
	}
}

func TestResumable_FalseWithoutSidecars(t *testing.T) {
	// A fresh temp dir has no run_state/sub_tasks/task_spec → not resumable,
	// regardless of whether bd is installed.
	root := t.TempDir()
	if _, ok := Resumable(context.Background(), root); ok {
		t.Error("expected not resumable for an empty workspace")
	}
}

func TestLoadResumeState_ErrorWhenMissing(t *testing.T) {
	ws := artifacts.Workspace{Root: t.TempDir(), Dir: t.TempDir()}
	tr := &TaskRunner{ws: ws}
	if _, _, _, err := tr.loadResumeState(); err == nil {
		t.Error("expected an error when resume artifacts are missing")
	}
}

func TestLoadResumeState_WorksWithoutRunState(t *testing.T) {
	// bd-less runs never write run_state.json; resume must still work from the
	// spec + sub-task plan alone.
	ws, _ := artifacts.EnsureWorkspace(t.TempDir())
	tr := &TaskRunner{ws: ws}

	spec := agent.TaskSpec{Title: "T", Description: "d"}
	specData, _ := json.Marshal(spec)
	if err := ws.WriteFile(artifacts.TaskSpecFile, specData); err != nil {
		t.Fatal(err)
	}
	if err := tr.writeSubTasks([]agent.SubTask{{Key: "T1", Title: "x"}}, nil); err != nil {
		t.Fatal(err)
	}

	gotSpec, st, plan, err := tr.loadResumeState()
	if err != nil {
		t.Fatalf("expected resume without run_state, got %v", err)
	}
	if gotSpec.Title != "T" || len(plan.Tasks) != 1 || st.TopBeadID != "" {
		t.Errorf("unexpected resume state: spec=%+v st=%+v plan=%+v", gotSpec, st, plan)
	}
}
