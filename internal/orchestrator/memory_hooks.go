package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/memory"
)

// memAppend writes a short timestamped entry to today's memory daily log.
// Best-effort: failures are logged via tr.event but never propagated.
//
// stage is a human-readable label (e.g. "task-start", "architect", "coder").
// body is the markdown payload appended under the entry header.
func (tr *TaskRunner) memAppend(stage, body string) {
	if !tr.cfg.Project.Context.Memory.Enabled {
		return
	}
	lay, err := memory.NewLayout(tr.ws.MemoryDir())
	if err != nil {
		tr.log.Warn("memory layout", "err", err)
		return
	}
	if err := lay.AppendDaily(time.Now(), stage, body); err != nil {
		tr.log.Warn("memory append daily", "err", err)
	}
}

// finaliseMemory is called once the pipeline reaches PipelineDone. It:
//  1. writes a per-task summary file in memory/tasks/
//  2. auto-promotes "Decisions" / "Constraints" bullets from architecture.md
//     and vision.md to MEMORY.md (deduplicated by hash)
//  3. records a final daily entry pointing to the artifacts
//
// All steps are best-effort and never fail the pipeline.
func (tr *TaskRunner) finaliseMemory(ctx context.Context, spec agent.TaskSpec) {
	_ = ctx // reserved for future async hooks (e.g. embedding refresh)
	if !tr.cfg.Project.Context.Memory.Enabled {
		return
	}
	lay, err := memory.NewLayout(tr.ws.MemoryDir())
	if err != nil {
		tr.log.Warn("memory finalise: layout", "err", err)
		return
	}

	taskID := taskIDFromSpec(spec)

	summary := tr.buildTaskSummary(spec)
	if err := lay.WriteTaskSummary(taskID, spec.Title, summary); err != nil {
		tr.log.Warn("memory finalise: write task summary", "err", err)
	}

	if tr.cfg.Project.Context.Memory.AutoPromote {
		promoted := tr.autoPromoteDecisions(lay, taskID)
		if promoted > 0 {
			tr.event(fmt.Sprintf("memory: promoted %d decision(s) to MEMORY.md", promoted))
		}
	}

	_ = lay.AppendDaily(time.Now(), "task-done",
		fmt.Sprintf("Task **%s** complete. Summary: `memory/tasks/%s.md`.", spec.Title, taskID))
}

// abortMemory is called on any error path (including panics) to preserve
// enough state on disk to reconstruct what the task was about and where it
// failed. It writes:
//  1. a "task-aborted" entry to today's daily log with the error,
//  2. a partial task summary in memory/tasks/ (whatever artifacts exist).
//
// Best-effort: never returns or panics.
func (tr *TaskRunner) abortMemory(spec agent.TaskSpec, stage string, taskErr error) {
	if !tr.cfg.Project.Context.Memory.Enabled {
		return
	}
	lay, err := memory.NewLayout(tr.ws.MemoryDir())
	if err != nil {
		tr.log.Warn("memory abort: layout", "err", err)
		return
	}

	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = "(no spec yet)"
	}
	taskID := taskIDFromSpec(spec)

	summary := tr.buildTaskSummary(spec)
	var sb strings.Builder
	sb.WriteString("> **Status:** aborted\n>\n")
	fmt.Fprintf(&sb, "> **Failed stage:** %s\n>\n", stage)
	fmt.Fprintf(&sb, "> **Error:** %v\n\n", taskErr)
	sb.WriteString(summary)
	if err := lay.WriteTaskSummary(taskID, title, sb.String()); err != nil {
		tr.log.Warn("memory abort: write task summary", "err", err)
	}

	_ = lay.AppendDaily(time.Now(), "task-aborted",
		fmt.Sprintf("Task **%s** aborted at stage `%s`: %v. Partial summary: `memory/tasks/%s.md`.",
			title, stage, taskErr, taskID))
}

// taskIDFromSpec derives a stable, filesystem-safe identifier from a TaskSpec.
func taskIDFromSpec(spec agent.TaskSpec) string {
	id := strings.TrimSpace(spec.Title)
	if id == "" {
		id = "task"
	}
	id = strings.ToLower(id)
	id = strings.ReplaceAll(id, " ", "-")
	// Prepend date so multiple tasks with the same title remain distinct.
	return time.Now().Format("20060102-1504") + "-" + id
}

func (tr *TaskRunner) buildTaskSummary(spec agent.TaskSpec) string {
	var sb strings.Builder
	if d := strings.TrimSpace(spec.Description); d != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(d)
		sb.WriteString("\n\n")
	}

	// Quote canonical artifacts directly so the memory summary is self-
	// contained and indexable even if the original files are later removed.
	for label, name := range map[string]string{
		"Vision":         artifacts.VisionFile,
		"Architecture":   artifacts.ArchitectureFile,
		"Plan":           artifacts.ImplementationPlanFile,
		"Changes":        artifacts.ChangesFile,
		"Review":         artifacts.ReviewFile,
		"UX review":      artifacts.UXReviewFile,
		"Security audit": artifacts.SecurityReviewFile,
		"Summary":        artifacts.SummaryFile,
	} {
		data, err := tr.ws.ReadFile(name)
		if err != nil || len(data) == 0 {
			continue
		}
		sb.WriteString("## ")
		sb.WriteString(label)
		sb.WriteString("\n\n")
		sb.WriteString(strings.TrimSpace(string(data)))
		sb.WriteString("\n\n")
	}
	if sb.Len() == 0 {
		sb.WriteString("_No artifacts produced._\n")
	}
	return sb.String()
}

// autoPromoteDecisions scans architecture.md and vision.md for bullets under
// sections matching "Decisions" / "Constraints" / "Principles" and appends
// them to MEMORY.md (deduplicated). Returns the number of newly promoted facts.
func (tr *TaskRunner) autoPromoteDecisions(lay memory.Layout, taskID string) int {
	var facts []memory.Fact
	sources := []struct {
		file, label string
	}{
		{artifacts.ArchitectureFile, "architecture.md"},
		{artifacts.VisionFile, "vision.md"},
	}
	for _, s := range sources {
		data, err := tr.ws.ReadFile(s.file)
		if err != nil || len(data) == 0 {
			continue
		}
		for _, item := range memory.ExtractDecisions(string(data), nil) {
			facts = append(facts, memory.Fact{Source: s.label, Text: item})
		}
	}
	if len(facts) == 0 {
		return 0
	}
	n, err := lay.PromoteFacts(time.Now(), taskID, facts)
	if err != nil {
		tr.log.Warn("memory finalise: promote facts", "err", err)
	}
	return n
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
for _, line := range strings.Split(s, "\n") {
t := strings.TrimSpace(line)
if t != "" {
return t
}
}
return ""
}
