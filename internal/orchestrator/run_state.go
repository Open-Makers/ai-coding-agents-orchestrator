package orchestrator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/beads"
)

// runStateSchemaVersion is bumped when the run_state.json shape changes so a
// resume can reject incompatible artifacts.
const runStateSchemaVersion = 1

// runState links the current workspace artifacts to the top-level orchestrator
// task bead, enabling resume detection (beads first, this file as cross-check).
type runState struct {
	SchemaVersion int       `json:"schema_version"`
	TopBeadID     string    `json:"top_bead_id"`
	RunID         string    `json:"run_id"`
	Title         string    `json:"title"`
	CreatedAt     time.Time `json:"created_at"`
}

// writeRunState persists the run state sidecar. Best-effort: returns any write
// error for the caller to log, never panics.
func (tr *TaskRunner) writeRunState(title string) error {
	st := runState{
		SchemaVersion: runStateSchemaVersion,
		TopBeadID:     tr.taskBeadID,
		RunID:         tr.runID,
		Title:         title,
		CreatedAt:     time.Now(),
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return tr.ws.WriteFile(artifacts.RunStateFile, data)
}

// readRunState loads the run state sidecar, if present.
func (tr *TaskRunner) readRunState() (runState, bool) {
	data, err := tr.ws.ReadFile(artifacts.RunStateFile)
	if err != nil {
		return runState{}, false
	}
	var st runState
	if err := json.Unmarshal(data, &st); err != nil {
		return runState{}, false
	}
	return st, true
}

// subTaskPlan is the structured decomposition persisted so a run can resume
// without re-decomposing. IDByKey maps each sub-task Key to its bead ID.
type subTaskPlan struct {
	Tasks   []agent.SubTask   `json:"tasks"`
	IDByKey map[string]string `json:"id_by_key"`
}

// writeSubTasks persists the structured sub-task plan sidecar.
func (tr *TaskRunner) writeSubTasks(tasks []agent.SubTask, idByKey map[string]string) error {
	data, err := json.MarshalIndent(subTaskPlan{Tasks: tasks, IDByKey: idByKey}, "", "  ")
	if err != nil {
		return err
	}
	return tr.ws.WriteFile(artifacts.SubTasksFile, data)
}

// readSubTasks loads the sub-task plan sidecar, if present.
func (tr *TaskRunner) readSubTasks() (subTaskPlan, bool) {
	data, err := tr.ws.ReadFile(artifacts.SubTasksFile)
	if err != nil {
		return subTaskPlan{}, false
	}
	var plan subTaskPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return subTaskPlan{}, false
	}
	return plan, true
}

// readSpec loads the approved TaskSpec from task_spec.json, if present.
func (tr *TaskRunner) readSpec() (agent.TaskSpec, bool) {
	data, err := tr.ws.ReadFile(artifacts.TaskSpecFile)
	if err != nil {
		return agent.TaskSpec{}, false
	}
	var spec agent.TaskSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return agent.TaskSpec{}, false
	}
	return spec, true
}

// loadResumeState reads the spec, run-state, and sub-task plan needed to resume
// an interrupted run. The spec and sub-task plan are required; the run-state
// sidecar (and its bead linkage) is optional so runs resume even when bd is
// unavailable. Returns an error when a required artifact is missing.
func (tr *TaskRunner) loadResumeState() (agent.TaskSpec, runState, subTaskPlan, error) {
	spec, ok := tr.readSpec()
	if !ok {
		return agent.TaskSpec{}, runState{}, subTaskPlan{}, errNoResumableTask
	}
	plan, ok := tr.readSubTasks()
	if !ok {
		return agent.TaskSpec{}, runState{}, subTaskPlan{}, errNoResumableTask
	}
	// run-state is best-effort: it carries the bead id / run id when present,
	// but a missing or bead-less sidecar still resumes from spec + sub-tasks.
	st, _ := tr.readRunState()
	return spec, st, plan, nil
}

// errNoResumableTask is returned when resume artifacts are missing.
var errNoResumableTask = errResume("no resumable task found")

type errResume string

func (e errResume) Error() string { return string(e) }

// ResumableTask describes an interrupted run that can be resumed.
type ResumableTask struct {
	TopBeadID string
	Title     string
}

// DoneTask is a completed top-level orchestrator task (history entry).
type DoneTask struct {
	ID    string
	Title string
}

// DoneTasks returns completed orchestrator tasks for the project, newest first
// as reported by beads. Returns nil when bd is unavailable or there are none.
func DoneTasks(ctx context.Context, root string) []DoneTask {
	issues, err := beads.ClosedTasks(ctx, root, labelOrchestratorTask)
	if err != nil || len(issues) == 0 {
		return nil
	}
	out := make([]DoneTask, 0, len(issues))
	for _, i := range issues {
		out = append(out, DoneTask{ID: i.ID, Title: i.Title})
	}
	return out
}

// Resumable reports whether root has an interrupted orchestrator task that can
// be resumed: an in_progress top-level orchestrator-task bead whose id matches
// the run-state sidecar, with the spec and sub-task plan still present.
// Returns (task, true) when resumable. Beads is the source of truth; the
// sidecars are a cross-check.
func Resumable(ctx context.Context, root string) (ResumableTask, bool) {
	ws := artifacts.Workspace{Root: root, Dir: filepath.Join(root, artifacts.DirName)}
	tr := &TaskRunner{ws: ws, root: root}

	st, ok := tr.readRunState()
	if !ok || st.TopBeadID == "" {
		return ResumableTask{}, false
	}
	if _, ok := tr.readSpec(); !ok {
		return ResumableTask{}, false
	}
	if _, ok := tr.readSubTasks(); !ok {
		return ResumableTask{}, false
	}

	// Cross-check beads: the top bead must still be an active (in_progress)
	// orchestrator task. When bd is unavailable, fall back to the sidecar.
	if beads.Available() {
		active, err := beads.ActiveTasks(ctx, root, labelOrchestratorTask)
		if err != nil {
			return ResumableTask{}, false
		}
		matched := false
		for _, a := range active {
			if a.ID == st.TopBeadID {
				matched = true
				break
			}
		}
		if !matched {
			return ResumableTask{}, false
		}
	}

	title := st.Title
	if title == "" {
		title = "interrupted task"
	}
	return ResumableTask{TopBeadID: st.TopBeadID, Title: title}, true
}
