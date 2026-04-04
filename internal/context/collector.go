package context

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// ProjectContext is a snapshot of the repository state passed to every agent.
type ProjectContext struct {
	Files         []string          // git ls-files
	RecentCommits []string          // git log --oneline -20
	UnstagedDiff  string            // git diff HEAD
	AlwaysInclude map[string]string // filename → content
}

// Collect gathers repository context from root using git commands.
func Collect(root string, cfg config.Config) (ProjectContext, error) {
	pc := ProjectContext{
		AlwaysInclude: make(map[string]string),
	}

	files, err := gitLines(root, "ls-files")
	if err != nil {
		return pc, fmt.Errorf("context: git ls-files: %w", err)
	}
	pc.Files = files

	commits, err := gitLines(root, "log", "--oneline", "-20")
	if err != nil {
		// non-fatal: new repo may have no commits
		commits = nil
	}
	pc.RecentCommits = commits

	diff, _ := gitOutput(root, "diff", "HEAD")
	pc.UnstagedDiff = diff

	rootAbs, _ := filepath.Abs(root)
	for _, cfgPath := range cfg.Project.Context.AlwaysInclude {
		// Only allow relative paths — no absolute or parent-escaping paths.
		if filepath.IsAbs(cfgPath) {
			continue
		}
		full := filepath.Join(rootAbs, cfgPath)
		// Verify the resolved path is still inside the repo root.
		if !strings.HasPrefix(full+string(filepath.Separator), rootAbs+string(filepath.Separator)) {
			continue
		}
		data, err := os.ReadFile(full)
		if err == nil {
			pc.AlwaysInclude[cfgPath] = string(data)
		}
	}

	return pc, nil
}

// SystemPromptFragment formats the project context as a block to inject into system prompts.
func (p ProjectContext) SystemPromptFragment() string {
	var sb strings.Builder

	sb.WriteString("## Repository Context\n\n")

	if len(p.Files) > 0 {
		limit := 50
		if len(p.Files) < limit {
			limit = len(p.Files)
		}
		sb.WriteString("### Files (first 50)\n```\n")
		sb.WriteString(strings.Join(p.Files[:limit], "\n"))
		sb.WriteString("\n```\n\n")
	}

	if len(p.RecentCommits) > 0 {
		sb.WriteString("### Recent Commits\n```\n")
		sb.WriteString(strings.Join(p.RecentCommits, "\n"))
		sb.WriteString("\n```\n\n")
	}

	if p.UnstagedDiff != "" {
		sb.WriteString("### Uncommitted Changes\n```diff\n")
		if len(p.UnstagedDiff) > 2000 {
			sb.WriteString(p.UnstagedDiff[:2000])
			sb.WriteString("\n... (truncated)")
		} else {
			sb.WriteString(p.UnstagedDiff)
		}
		sb.WriteString("\n```\n\n")
	}

	for name, content := range p.AlwaysInclude {
		sb.WriteString(fmt.Sprintf("### %s\n```\n", name))
		if len(content) > 3000 {
			sb.WriteString(content[:3000])
			sb.WriteString("\n... (truncated)")
		} else {
			sb.WriteString(content)
		}
		sb.WriteString("\n```\n\n")
	}

	return sb.String()
}

func gitLines(root string, args ...string) ([]string, error) {
	out, err := gitOutput(root, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
