package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// reviewArtifacts lists the curated top-level .orchestrator files the PM should
// read to resume a project, in priority order. Transient raw agent dumps are
// excluded to keep the digest concise.
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

// maxMemoryFiles caps how many recent memory task files are embedded so the seed
// doesn't balloon when a project has a long history.
const maxMemoryFiles = 3

// HasReviewableArtifacts reports whether the project's .orchestrator workspace
// holds any planning artifacts the PM could use to resume the project. This
// includes the curated top-level files AND the persistent task memory under
// .orchestrator/memory/ (which is what survives after a completed/failed run).
func HasReviewableArtifacts(root string) bool {
	ws := artifacts.Workspace{Root: root, Dir: filepath.Join(root, artifacts.DirName)}
	for _, name := range reviewArtifacts {
		if data, err := ws.ReadFile(name); err == nil && strings.TrimSpace(string(data)) != "" {
			return true
		}
	}
	return len(memoryFiles(ws.Dir)) > 0
}

// memoryFiles returns markdown files under .orchestrator/memory, sorted so the
// most recent (by filename, which is timestamp-prefixed) come last.
func memoryFiles(wsDir string) []string {
	memDir := filepath.Join(wsDir, artifacts.MemoryDirName)
	var out []string
	_ = filepath.WalkDir(memDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// BuildProjectReviewSeed reads the key .orchestrator artifacts (curated files
// plus recent task memory) and produces a task-input string that instructs the
// PM to comb the existing planning files, compare them against the current
// repository state, and plan ONLY the work that remains. Returns "" when no
// artifacts are present.
func BuildProjectReviewSeed(root string) string {
	ws := artifacts.Workspace{Root: root, Dir: filepath.Join(root, artifacts.DirName)}

	var body strings.Builder
	found := 0

	for _, name := range reviewArtifacts {
		data, err := ws.ReadFile(name)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		found++
		fmt.Fprintf(&body, "\n### %s\n\n```\n%s\n```\n", name, tokenutil.Truncate(content, perArtifactMaxTokens))
	}

	// Include the most recent task-memory files; these persist even after the
	// curated artifacts have been cleaned, and capture the PM's decisions.
	mem := memoryFiles(ws.Dir)
	if len(mem) > maxMemoryFiles {
		mem = mem[len(mem)-maxMemoryFiles:]
	}
	for _, path := range mem {
		data, err := os.ReadFile(path) // #nosec G304 -- path discovered under .orchestrator/memory
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		found++
		rel := filepath.Join(artifacts.DirName, artifacts.MemoryDirName, filepath.Base(path))
		fmt.Fprintf(&body, "\n### %s\n\n```\n%s\n```\n", rel, tokenutil.Truncate(content, perArtifactMaxTokens))
	}

	if found == 0 {
		return ""
	}

	var seed strings.Builder
	seed.WriteString("Resume this project. It was already planned and partially implemented in a previous run.\n\n")
	seed.WriteString("Your job: read the existing planning artifacts below (saved under `.orchestrator/`), ")
	seed.WriteString("inspect the current repository state, and determine what still needs to be implemented. ")
	seed.WriteString("Do NOT restart from scratch and do NOT redo work that is already complete. ")
	seed.WriteString("Produce a TASKSPEC and decomposition covering ONLY the remaining work needed to finish the project.\n")
	seed.WriteString("\nExisting `.orchestrator/` artifacts (truncated):\n")
	seed.WriteString(body.String())
	seed.WriteString("\nFull artifacts (including all memory under `.orchestrator/memory/`) are available in the repository if you need more detail.\n")
	return seed.String()
}
