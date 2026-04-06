package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

// maxSourceFileSize limits how large a single source file can be before truncation.
const maxSourceFileSize = 4000

// maxTotalSourceContext caps total source content injected into prompts.
const maxTotalSourceContext = 30000

// maxSourceFiles limits how many source files are included in full context.
const maxSourceFiles = 20

// ProjectContext is a snapshot of the repository state passed to every agent.
type ProjectContext struct {
	Files         []string          // git ls-files
	RecentCommits []string          // git log --oneline -20
	UnstagedDiff  string            // git diff HEAD
	AlwaysInclude map[string]string // filename → content
	SourceFiles   map[string]string // key source files → content (for brownfield)
	IsBrownfield  bool              // true if project has existing source code
	ProjectType   string            // detected project type (go, node, rust, etc.)
	TreeStructure string            // directory tree summary
}

// Collect gathers repository context from root using git commands.
func Collect(root string, cfg config.Config) (ProjectContext, error) {
	pc := ProjectContext{
		AlwaysInclude: make(map[string]string),
		SourceFiles:   make(map[string]string),
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
		data, err := safefile.ReadFile(rootAbs, cfgPath)
		if err == nil {
			pc.AlwaysInclude[cfgPath] = string(data)
		}
	}

	pc.ProjectType = detectProjectType(rootAbs)
	pc.IsBrownfield = detectBrownfield(files, pc.ProjectType)
	pc.TreeStructure = buildTreeStructure(files)

	if pc.IsBrownfield {
		pc.SourceFiles = collectSourceFiles(rootAbs, files)
	}

	return pc, nil
}

// SystemPromptFragment formats the project context as a block to inject into system prompts.
func (p ProjectContext) SystemPromptFragment() string {
	var sb strings.Builder

	sb.WriteString("## Repository Context\n\n")

	if p.IsBrownfield {
		sb.WriteString("### ⚠️ BROWNFIELD PROJECT — EXISTING CODEBASE\n")
		sb.WriteString("This is an EXISTING project with working code. You MUST:\n")
		sb.WriteString("- Analyze and understand the existing code structure before making changes\n")
		sb.WriteString("- MODIFY existing files instead of creating new parallel structures\n")
		sb.WriteString("- Preserve existing APIs, interfaces, and patterns unless explicitly asked to change them\n")
		sb.WriteString("- Reuse existing packages, types, and functions\n")
		sb.WriteString("- Do NOT create duplicate directories or files that mirror existing ones\n\n")
	}

	if p.ProjectType != "" {
		_, err := fmt.Fprintf(&sb, "### Project Type: %s\n\n", p.ProjectType)
		if err != nil {
			return ""
		}
	}

	if p.TreeStructure != "" {
		sb.WriteString("### Project Structure\n```\n")
		sb.WriteString(p.TreeStructure)
		sb.WriteString("\n```\n\n")
	}

	if len(p.Files) > 0 {
		limit := 80
		if len(p.Files) < limit {
			limit = len(p.Files)
		}
		_, err := fmt.Fprintf(&sb, "### Files (%d total, showing first %d)\n```\n", len(p.Files), limit)
		if err != nil {
			return ""
		}
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

	// Existing source files — critical for brownfield understanding.
	if len(p.SourceFiles) > 0 {
		sb.WriteString("### Existing Source Code\n")
		sb.WriteString("Below are the key source files in the project. Study them carefully before making changes.\n\n")
		for name, content := range p.SourceFiles {
			_, err := fmt.Fprintf(&sb, "**%s**\n```\n", name)
			if err != nil {
				return ""
			}
			if len(content) > maxSourceFileSize {
				sb.WriteString(content[:maxSourceFileSize])
				sb.WriteString("\n... (truncated)")
			} else {
				sb.WriteString(content)
			}
			sb.WriteString("\n```\n\n")
		}
	}

	for name, content := range p.AlwaysInclude {
		_, err := fmt.Fprintf(&sb, "### %s\n```\n", name)
		if err != nil {
			return ""
		}
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

// detectProjectType identifies the project language/framework from marker files.
func detectProjectType(root string) string {
	markers := []struct {
		file     string
		projType string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"package.json", "node"},
		{"pom.xml", "java-maven"},
		{"build.gradle", "java-gradle"},
		{"pyproject.toml", "python"},
		{"requirements.txt", "python"},
		{"Gemfile", "ruby"},
		{"mix.exs", "elixir"},
	}
	for _, m := range markers {
		if fileExists(filepath.Join(root, m.file)) {
			return m.projType
		}
	}
	return ""
}

// detectBrownfield returns true if the project already has meaningful source code.
func detectBrownfield(files []string, _ string) bool {
	sourceCount := 0
	for _, f := range files {
		if isSourceFile(f) {
			sourceCount++
		}
	}
	// A project with 2+ source files is considered brownfield.
	return sourceCount >= 2
}

// isSourceFile returns true for files that contain application logic.
func isSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	sourceExts := map[string]bool{
		".go": true, ".rs": true, ".py": true, ".js": true, ".ts": true,
		".tsx": true, ".jsx": true, ".java": true, ".rb": true, ".ex": true,
		".exs": true, ".cs": true, ".cpp": true, ".c": true, ".h": true,
		".swift": true, ".kt": true, ".scala": true, ".vue": true, ".svelte": true,
	}
	if !sourceExts[ext] {
		return false
	}
	// Exclude test files, generated files, vendor, etc.
	lower := strings.ToLower(path)
	if strings.Contains(lower, "vendor/") || strings.Contains(lower, "node_modules/") ||
		strings.Contains(lower, ".git/") {
		return false
	}
	return true
}

// collectSourceFiles reads key source files from the project to provide
// existing code context to agents. Prioritizes entry points, core packages,
// and non-test source files.
func collectSourceFiles(root string, files []string) map[string]string {
	ranked := rankSourceFiles(files)
	result := make(map[string]string)
	totalSize := 0

	for _, path := range ranked {
		if len(result) >= maxSourceFiles {
			break
		}
		if totalSize >= maxTotalSourceContext {
			break
		}
		content, err := safefile.ReadFile(root, path)
		if err != nil {
			continue
		}
		size := len(content)
		if size == 0 || size > maxSourceFileSize*2 {
			continue
		}
		result[path] = string(content)
		totalSize += size
	}

	return result
}

// rankSourceFiles orders files by importance for brownfield context.
// Entry points and core packages come first, tests last.
func rankSourceFiles(files []string) []string {
	type scored struct {
		path  string
		score int
	}

	var source []scored
	for _, f := range files {
		if !isSourceFile(f) {
			continue
		}
		s := 50 // base score
		lower := strings.ToLower(f)

		// Entry points are highest priority.
		if strings.Contains(lower, "main.go") || strings.Contains(lower, "main.py") ||
			strings.Contains(lower, "main.rs") || strings.Contains(lower, "main.ts") ||
			strings.Contains(lower, "main.js") || strings.Contains(lower, "app.go") ||
			strings.Contains(lower, "app.py") || strings.Contains(lower, "app.ts") {
			s = 100
		}

		// Core/internal packages are high priority.
		if strings.Contains(lower, "internal/") || strings.Contains(lower, "core/") ||
			strings.Contains(lower, "pkg/") || strings.Contains(lower, "lib/") {
			s += 20
		}

		// Config files are useful context.
		if strings.Contains(lower, "config") {
			s += 10
		}

		// cmd/ entry points.
		if strings.HasPrefix(lower, "cmd/") {
			s += 15
		}

		// Test files are lower priority — still useful but include last.
		if strings.Contains(lower, "_test.go") || strings.Contains(lower, "_test.") ||
			strings.Contains(lower, ".test.") || strings.Contains(lower, "test_") {
			s -= 30
		}

		source = append(source, scored{path: f, score: s})
	}

	sort.Slice(source, func(i, j int) bool {
		if source[i].score != source[j].score {
			return source[i].score > source[j].score
		}
		return source[i].path < source[j].path
	})

	result := make([]string, len(source))
	for i, s := range source {
		result[i] = s.path
	}
	return result
}

// buildTreeStructure creates a compact directory tree from file list.
func buildTreeStructure(files []string) string {
	dirs := make(map[string]bool)
	for _, f := range files {
		dir := filepath.Dir(f)
		for dir != "." && dir != "" {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}

	if len(dirs) == 0 {
		return ""
	}

	sorted := make([]string, 0, len(dirs))
	for d := range dirs {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)

	var sb strings.Builder
	for _, d := range sorted {
		depth := strings.Count(d, string(filepath.Separator))
		indent := strings.Repeat("  ", depth)
		name := filepath.Base(d)
		_, err := fmt.Fprintf(&sb, "%s%s/\n", indent, name)
		if err != nil {
			return ""
		}
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
	cmd := executil.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
