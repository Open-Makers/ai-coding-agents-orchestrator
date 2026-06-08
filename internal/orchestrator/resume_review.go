package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// reviewArtifacts lists the .orchestrator files the PM should read to resume a
// project, in priority order. Transient raw agent dumps and the bulk memory
// directory are intentionally excluded to keep the digest concise.
var reviewArtifacts = []string{
	artifacts.RequirementsFile,
	artifacts.VisionFile,
	artifacts.MoscowFile,
	artifacts.ArchitectureFile,
	artifacts.ImplementationPlanFile,
	artifacts.TaskSpecFile,
	artifacts.SubTasksFile,
	artifacts.SummaryFile,
	artifacts.ReviewFile,
	artifacts.UXReviewFile,
	artifacts.SecurityReviewFile,
}

// perArtifactMaxTokens bounds how much of each artifact is embedded so the seed
// stays well within a local model's context window.
const perArtifactMaxTokens = 700

// HasReviewableArtifacts reports whether the project's .orchestrator workspace
// holds any planning artifacts the PM could use to resume the project.
func HasReviewableArtifacts(root string) bool {
	ws := artifacts.Workspace{Root: root, Dir: filepath.Join(root, artifacts.DirName)}
	for _, name := range reviewArtifacts {
		if data, err := ws.ReadFile(name); err == nil && strings.TrimSpace(string(data)) != "" {
			return true
		}
	}
	return false
}

// BuildProjectReviewSeed reads the key .orchestrator artifacts and produces a
// task-input string that instructs the PM to comb the existing planning files,
// compare them against the current repository state, and plan ONLY the work that
// remains. It returns "" when no artifacts are present.
func BuildProjectReviewSeed(root string) string {
	ws := artifacts.Workspace{Root: root, Dir: filepath.Join(root, artifacts.DirName)}

	var found []string
	var sb strings.Builder
	for _, name := range reviewArtifacts {
		data, err := ws.ReadFile(name)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		found = append(found, name)
		fmt.Fprintf(&sb, "\n### %s\n\n", name)
		fmt.Fprintf(&sb, "```\n%s\n```\n", tokenutil.Truncate(content, perArtifactMaxTokens))
	}

	if len(found) == 0 {
		return ""
	}

	var seed strings.Builder
	seed.WriteString("Resume this project. It was already planned and partially implemented in a previous run.\n\n")
	seed.WriteString("Your job: read the existing planning artifacts below (saved under `.orchestrator/`), ")
	seed.WriteString("inspect the current repository state, and determine what still needs to be implemented. ")
	seed.WriteString("Do NOT restart from scratch and do NOT redo work that is already complete. ")
	seed.WriteString("Produce a TASKSPEC and decomposition covering ONLY the remaining work needed to finish the project.\n")
	seed.WriteString("\nExisting `.orchestrator/` artifacts (truncated):\n")
	seed.WriteString(sb.String())
	seed.WriteString("\nFull artifacts (including memory under `.orchestrator/memory/`) are available in the repository if you need more detail.\n")
	return seed.String()
}
