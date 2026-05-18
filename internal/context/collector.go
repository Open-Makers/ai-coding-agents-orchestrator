package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/chunker"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/embedder"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/index"
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
	Files               []string          // git ls-files
	RecentCommits       []string          // git log --oneline -20
	UnstagedDiff        string            // git diff HEAD
	AlwaysInclude       map[string]string // filename → content
	SourceFiles         map[string]string // key source files → content (for brownfield)
	IsBrownfield        bool              // true if project has existing source code
	ProjectType         string            // detected project type (go, node, rust, etc.)
	TreeStructure       string            // directory tree summary
	ProgrammingLanguage string            // explicit language setting from project config

	// idx is the optional semantic index (nil when disabled or unavailable).
	idx index.Index
}

// Collect gathers repository context from root using git commands.
func Collect(root string, cfg config.Config) (ProjectContext, error) {
	pc := ProjectContext{
		AlwaysInclude: make(map[string]string),
		SourceFiles:   make(map[string]string),
	}

	files, err := gitLines(root, gitArgsWithExcludes("ls-files")...)
	if err != nil {
		return pc, fmt.Errorf("context: git ls-files: %w", err)
	}
	pc.Files = filterInternalPaths(files, cfg.Project.Context.ExcludePatterns...)

	commits, err := gitLines(root, "log", "--oneline", "-20")
	if err != nil {
		// non-fatal: new repo may have no commits
		commits = nil
	}
	pc.RecentCommits = commits

	diff, _ := gitOutput(root, gitArgsWithExcludes("diff", "HEAD")...)
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
	pc.IsBrownfield = detectBrownfield(pc.Files, pc.ProjectType)
	pc.TreeStructure = buildTreeStructure(pc.Files)
	pc.ProgrammingLanguage = cfg.Project.Language

	if pc.IsBrownfield {
		pc.SourceFiles = collectSourceFiles(rootAbs, pc.Files, nil)
	}

	if cfg.Project.Context.SemanticIndex.Enabled {
		emb, err := embedderFactory(cfg.Project.Context.SemanticIndex)
		if err == nil {
			if idx, idxErr := buildOrRefreshIndex(rootAbs, pc.Files, emb); idxErr == nil {
				pc.idx = idx
			}
		}
	}

	return pc, nil
}

// embedderFactory is overridable in tests to inject a fake embedder.
var embedderFactory = func(cfg config.SemanticIndexConfig) (embedder.Embedder, error) {
	return embedder.New(cfg)
}

// SetEmbedderFactory swaps the embedder constructor used by Collect when the
// semantic index is enabled. Returns the previous factory so tests can
// restore it. Intended for tests only — production callers should leave it
// alone and rely on configuration.
func SetEmbedderFactory(f func(config.SemanticIndexConfig) (embedder.Embedder, error)) func(config.SemanticIndexConfig) (embedder.Embedder, error) {
	prev := embedderFactory
	embedderFactory = f
	return prev
}

// ContextProfile controls how much detail is included in the system prompt fragment.
type ContextProfile int

const (
	// ProfileFull includes everything: files, commits, diffs, source code.
	// Used by PM, Planner, and Coder where full context is needed.
	ProfileFull ContextProfile = iota

	// ProfileCompact includes only project type and tree structure.
	// Used by review agents (reviewer, security, QA, UX) that receive
	// source code separately through their payload, saving tokens on cloud models.
	ProfileCompact
)

// SystemPromptFragment formats the project context as a block to inject into system prompts.
// Use ProfileFull for agents that need complete context (PM, planner, coder).
// Use ProfileCompact for review agents that receive source code separately.
func (p ProjectContext) SystemPromptFragment(profile ...ContextProfile) string {
	prof := ProfileFull
	if len(profile) > 0 {
		prof = profile[0]
	}

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

	if p.ProgrammingLanguage != "" {
		_, _ = fmt.Fprintf(&sb, "### ⚡ Programming Language: %s\n", strings.ToUpper(p.ProgrammingLanguage))
		sb.WriteString("ALL generated code MUST be written in " + p.ProgrammingLanguage + ".\n")
		sb.WriteString("Use " + p.ProgrammingLanguage + " idioms, conventions, and ecosystem tools.\n")
		sb.WriteString("Do NOT generate code in any other language unless explicitly required (e.g. Makefile, Dockerfile).\n\n")
	}

	if p.ProjectType != "" {
		_, _ = fmt.Fprintf(&sb, "### Project Type: %s\n\n", p.ProjectType)
	}

	if p.TreeStructure != "" {
		sb.WriteString("### Project Structure\n```\n")
		sb.WriteString(p.TreeStructure)
		sb.WriteString("\n```\n\n")
	}

	// Compact profile stops here — review agents don't need file lists,
	// commits, diffs, or source code (they get source via payload).
	if prof == ProfileCompact {
		return sb.String()
	}

	if len(p.Files) > 0 {
		limit := 80
		if len(p.Files) < limit {
			limit = len(p.Files)
		}
		_, _ = fmt.Fprintf(&sb, "### Files (%d total, showing first %d)\n```\n", len(p.Files), limit)
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
			_, _ = fmt.Fprintf(&sb, "**%s**\n```\n", name)
			if len(content) > maxSourceFileSize {
				// Defensive cap: collectSourceFiles already trims at chunk
				// boundaries, but very large always-included files still
				// get truncated here at byte level.
				sb.WriteString(content[:maxSourceFileSize])
				sb.WriteString("\n... (truncated)")
			} else {
				sb.WriteString(content)
			}
			sb.WriteString("\n```\n\n")
		}
	}

	for name, content := range p.AlwaysInclude {
		_, _ = fmt.Fprintf(&sb, "### %s\n```\n", name)
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

// SourceEntry holds a source file's content as injected into the context.
// Truncated indicates the body was reduced from the original by chunk-level
// selection (whole functions/types) rather than included verbatim.
type SourceEntry struct {
	Content   string
	Truncated bool
}

// collectSourceFiles reads key source files from the project to provide
// existing code context to agents. Prioritizes entry points, core packages,
// and non-test source files. Optional seeds boost scoring for files that the
// caller knows are relevant (e.g. files referenced by a failing test).
func collectSourceFiles(root string, files []string, seeds []string) map[string]string {
	ranked := rankSourceFiles(root, files, seeds)
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
		if size == 0 {
			continue
		}
		// Reduce oversized files at chunk boundaries instead of dropping them.
		if size > maxSourceFileSize*2 {
			reduced := selectChunksWithinBudget(path, content, maxSourceFileSize)
			if reduced == "" {
				continue
			}
			result[path] = reduced
			totalSize += len(reduced)
			continue
		}
		result[path] = string(content)
		totalSize += size
	}

	return result
}

// selectChunksWithinBudget returns whole-chunk source up to budget, ordered
// by inclusion priority (exported decls first). The returned string ends with
// a `... (chunks omitted)` marker when at least one chunk was dropped.
func selectChunksWithinBudget(path string, content []byte, budget int) string {
	chunks, err := chunker.Split(path, content)
	if err != nil || len(chunks) == 0 {
		return ""
	}
	chunker.SortByPriority(chunks)
	var sb strings.Builder
	used := 0
	included := 0
	for _, c := range chunks {
		body := c.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if used+len(body) > budget && included > 0 {
			break
		}
		sb.WriteString(body)
		sb.WriteString("\n")
		used += len(body) + 1
		included++
		if used >= budget {
			break
		}
	}
	if included < len(chunks) {
		sb.WriteString("... (chunks omitted)")
	}
	return sb.String()
}

// rankSourceFiles orders files by importance for brownfield context.
// Entry points and core packages come first, tests last. When seeds are
// provided, the seed files (and their imports / callers of their primary
// exported symbol) get a substantial score boost.
func rankSourceFiles(root string, files []string, seeds []string) []string {
	boosts := computeSeedBoosts(root, seeds)

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

		s += boosts[f]

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

// computeSeedBoosts assigns score deltas to seed files, their imports, and
// their callers. Heavier weight goes to the seed itself.
func computeSeedBoosts(root string, seeds []string) map[string]int {
	boosts := make(map[string]int)
	if root == "" || len(seeds) == 0 {
		return boosts
	}
	for _, seed := range seeds {
		boosts[seed] += 200
		if imps, err := ImportsOf(root, seed); err == nil {
			for _, p := range imps {
				boosts[p] += 80
			}
		}
		sym := PrimarySymbolOf(root, seed)
		if sym == "" {
			continue
		}
		if callers, err := CallersOf(root, sym); err == nil {
			for _, p := range callers {
				if p == seed {
					continue
				}
				boosts[p] += 60
			}
		}
	}
	return boosts
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

// excludedTopLevelDirs are directories whose contents must never be exposed
// as project context — they are tooling/state, not the user's source code.
var excludedTopLevelDirs = []string{
	".orchestrator/",
	".git/",
	"node_modules/",
	"vendor/",
	"dist/",
	"build/",
	"target/",
	"out/",
	".next/",
	".nuxt/",
	".cache/",
	".parcel-cache/",
	"__pycache__/",
	".venv/",
	"venv/",
	".tox/",
	"coverage/",
	".coverage/",
	"tmp/",
	".idea/",
	".vscode/",
}

// noiseFileNames are exact file basenames that pollute context (lockfiles).
var noiseFileNames = map[string]bool{
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"Cargo.lock":        true,
	"Gemfile.lock":      true,
	"poetry.lock":       true,
}

// isNoiseFile reports whether a path is generated/non-source noise that should
// be excluded from context (lockfiles, minified assets, source maps).
// Note: go.sum is intentionally NOT listed — it is often required.
func isNoiseFile(path string) bool {
	base := filepath.Base(path)
	if noiseFileNames[base] {
		return true
	}
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".min.js"),
		strings.HasSuffix(lower, ".min.css"),
		strings.HasSuffix(lower, ".map"):
		return true
	}
	return false
}

// gitArgsWithExcludes appends pathspec exclusions so git commands (ls-files,
// diff) never surface paths under excludedTopLevelDirs (matched at any depth).
func gitArgsWithExcludes(args ...string) []string {
	args = append(args, "--", ".")
	for _, dir := range excludedTopLevelDirs {
		name := strings.TrimSuffix(dir, "/")
		args = append(args, ":(exclude,glob)"+name+"/**")
		args = append(args, ":(exclude,glob)**/"+name+"/**")
	}
	return args
}

// filterInternalPaths removes paths under directories that should not be part
// of the project review (orchestrator state, VCS internals, dependency dirs),
// noise files (lockfiles, minified assets), and any user-supplied glob patterns.
func filterInternalPaths(files []string, extraPatterns ...string) []string {
	if len(files) == 0 {
		return files
	}
	out := files[:0:0]
	for _, f := range files {
		if isInternalPath(f) {
			continue
		}
		if isNoiseFile(f) {
			continue
		}
		if matchesAnyPattern(f, extraPatterns) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func matchesAnyPattern(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	base := filepath.Base(path)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}

func isInternalPath(path string) bool {
	normalized := filepath.ToSlash(path)
	for _, prefix := range excludedTopLevelDirs {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
		// Also exclude when the directory appears as any path segment.
		if strings.Contains(normalized, "/"+prefix) {
			return true
		}
	}
	return false
}
