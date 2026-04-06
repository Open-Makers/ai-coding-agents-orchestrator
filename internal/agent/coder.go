package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// CoderPayload is the request for initial code generation.
type CoderPayload struct {
	Plan           string
	ProjectContext string
	Scope          []string
	StageName      string   // human-readable stage label (e.g. "Stage 2/5: Must Have — Auth")
	StageIndex     int      // 1-based stage number (0 = monolithic)
	TotalStages    int      // total number of stages
	PriorFiles     []string // files written by previous stages (read from disk for context)
}

// CoderFixPayload is the request for fixing code based on test/review failures.
type CoderFixPayload struct {
	Failure        string
	ProjectContext string
	Files          []string // source files to include as context (read from disk)
}

// CoderResult holds the list of files written by the coder.
type CoderResult struct {
	Files []string
}

// CoderAgent generates code and writes files to disk one by one.
type CoderAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	root   string
	cfg    config.Config
	exec   *executil.Runner
	skills []string
	model  string
}

func NewCoderAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, root string, cfg config.Config, skills []string, model string) *CoderAgent {
	return &CoderAgent{
		BaseAgent: NewBase(bus.RoleCoder, b),
		runner:    r,
		ws:        ws,
		root:      root,
		cfg:       cfg,
		exec:      executil.NewRunner(root),
		skills:    skills,
		model:     model,
	}
}

func (a *CoderAgent) Role() bus.AgentRole { return bus.RoleCoder }

func (a *CoderAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	systemPrompt, userContent, err := a.buildPrompt(msg)
	if err != nil {
		return bus.Message{}, err
	}

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("coder: runner: %w", err)
	}

	written, fullOutput, err := a.streamAndWriteFiles(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("coder: stream: %w", err)
	}

	// Always save raw output for debugging, even if no files were extracted.
	if err := a.ws.WriteFile(artifacts.RawCoderOutputFile, []byte(fullOutput+"\n")); err != nil {
		return bus.Message{}, err
	}

	sections := parseSections(fullOutput, "CHANGES", "TEST_CMDS")

	if err := a.ws.WriteFile(artifacts.ChangesFile, []byte(sections["CHANGES"]+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.TestCmdsFile, []byte(sections["TEST_CMDS"]+"\n")); err != nil {
		return bus.Message{}, err
	}

	// Initial code generation must produce at least one file.
	if _, isInitial := msg.Payload.(CoderPayload); isInitial && len(written) == 0 {
		return bus.Message{}, fmt.Errorf("coder: no file blocks found in initial code generation output (raw output saved to %s)", artifacts.RawCoderOutputFile)
	}

	// Ensure go.mod exists immediately after writing files so that
	// subsequent steps (tester, go mod tidy) can find it.
	a.ensureGoMod()

	a.Bus.Publish(bus.NewMessage(bus.RoleCoder, "", bus.MsgEvent, "EventFilesWritten"))

	return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, CoderResult{Files: written}), nil
}

// streamAndWriteFiles consumes the LLM token stream, detects code fence blocks,
// and writes each file to disk immediately when a fence closes.
func (a *CoderAgent) streamAndWriteFiles(ch <-chan runner.Token) ([]string, string, error) {
	var fullOutput strings.Builder
	var lineBuf strings.Builder
	var recentLines []string // sliding window for path detection
	var contentLines []string
	var written []string

	inFence := false
	currentPath := ""

	isFence := func(trimmed string) bool {
		return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
	}

	fenceTag := func(trimmed string) string {
		if strings.HasPrefix(trimmed, "```") {
			return strings.TrimPrefix(trimmed, "```")
		}
		return strings.TrimPrefix(trimmed, "~~~")
	}

	flushLine := func(line string) {
		fullOutput.WriteString(line)
		fullOutput.WriteByte('\n')

		trimmed := strings.TrimSpace(line)

		if isFence(trimmed) {
			if inFence {
				// Fence closing — write file if we have a path.
				if currentPath == "" {
					currentPath = guessFilePathFromContent(contentLines)
				}
				if currentPath != "" {
					content := strings.Join(contentLines, "\n") + "\n"
					if err := a.writeOneFile(currentPath, content); err != nil {
						a.emitToken(fmt.Sprintf("error writing %s: %v\n", currentPath, err), false)
					} else {
						written = append(written, currentPath)
						a.emitToken(fmt.Sprintf("wrote: %s\n", currentPath), false)
					}
				}
				inFence = false
				contentLines = nil
				currentPath = ""
			} else {
				// Fence opening — find path from recent lines or fence tag.
				currentPath = findPathInRecent(recentLines)
				if currentPath == "" {
					tag := fenceTag(trimmed)
					currentPath = pathFromFenceTag(tag)
				}
				inFence = true
				contentLines = nil
			}
		} else if inFence {
			contentLines = append(contentLines, line)
		}

		// Keep sliding window of non-fence lines for path detection.
		if !isFence(trimmed) {
			recentLines = append(recentLines, line)
			if len(recentLines) > 8 {
				recentLines = recentLines[1:]
			}
		}
	}

	for tok := range ch {
		if tok.Error != nil {
			return written, fullOutput.String(), tok.Error
		}
		if tok.Done {
			break
		}
		a.emitToken(tok.Text, false)
		lineBuf.WriteString(tok.Text)

		// Process complete lines.
		for {
			content := lineBuf.String()
			idx := strings.Index(content, "\n")
			if idx < 0 {
				break
			}
			line := content[:idx]
			lineBuf.Reset()
			lineBuf.WriteString(content[idx+1:])
			flushLine(line)
		}
	}

	// Flush remaining partial line.
	if lineBuf.Len() > 0 {
		flushLine(lineBuf.String())
	}

	a.emitToken("", true)
	return written, fullOutput.String(), nil
}

// writeOneFile writes a single file to disk under the project root.
func (a *CoderAgent) writeOneFile(path, content string) error {
	path = sanitizeFilePath(path, a.root)
	if path == "" {
		return fmt.Errorf("empty path after sanitization")
	}
	target := filepath.Join(a.root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", path, err)
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

// findPathInRecent scans recent non-fence lines (newest first) for a file path.
func findPathInRecent(lines []string) string {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		trim := strings.TrimSpace(lines[i])
		if trim == "" {
			continue
		}
		if p := parseFilePath(trim); p != "" {
			return p
		}
	}
	return ""
}

// buildPrompt constructs system and user prompts depending on the payload type.
func (a *CoderAgent) buildPrompt(msg bus.Message) (string, string, error) {
	switch payload := msg.Payload.(type) {
	case CoderPayload:
		moduleInfo := ""
		if a.cfg.Project.ModulePath != "" {
			moduleInfo = fmt.Sprintf("\nThe Go module path is: %s\nUse this module path for ALL internal imports (e.g. %s/internal/pkg).\nDo NOT include go.mod — it is already initialised.\n", a.cfg.Project.ModulePath, a.cfg.Project.ModulePath)
		}

		projectName := a.cfg.Project.Name
		if projectName == "" {
			projectName = filepath.Base(a.root)
		}

		system := fmt.Sprintf(prompts.MustLoad("coder-initial"), moduleInfo, projectName, projectName, payload.ProjectContext)

		var userParts []string

		if payload.StageIndex > 0 {
			userParts = append(userParts, fmt.Sprintf(
				"You are implementing %s.\nImplement ONLY this stage's scope. Do NOT implement features from other stages.\nOutput source code files ONLY — no explanations.",
				payload.StageName))
		} else {
			userParts = append(userParts, "Implement the following plan. Output source code files ONLY — no explanations.\nIMPORTANT: Implement ALL features marked as \"Must Have\" and \"Should Have\". Skip \"Could Have\" and \"Won't Have\" items.")
		}

		userParts = append(userParts, fmt.Sprintf("Plan:\n%s", payload.Plan))

		// Include existing project files for brownfield context.
		existingFiles := a.collectExistingSourceFiles()
		if len(existingFiles) > 0 {
			sourceCtx := a.buildSourceContext(existingFiles)
			if sourceCtx != "" {
				userParts = append(userParts, fmt.Sprintf(
					"EXISTING PROJECT FILES — you MUST modify these files, not create new ones with different paths:\n%s",
					sourceCtx))
			}
		}

		if len(payload.PriorFiles) > 0 {
			sourceCtx := a.buildSourceContext(filterSourceFiles(payload.PriorFiles))
			if sourceCtx != "" {
				userParts = append(userParts, fmt.Sprintf(
					"Files from previous stages (modify only if needed for this stage):\n%s",
					sourceCtx))
			}
		}

		user := strings.Join(userParts, "\n\n")
		return system, user, nil

	case CoderFixPayload:
		changes, _ := a.ws.ReadFile(artifacts.ChangesFile)

		// Build source context from actual files on disk.
		sourceContext := a.buildSourceContext(payload.Files)

		system := fmt.Sprintf(prompts.MustLoad("coder-fix"), payload.ProjectContext)

		user := fmt.Sprintf("Error output:\n%s\n\nPrevious changes summary:\n%s\n\nCurrent source files:\n%s\n\nFix the failing code and output the corrected files.",
			payload.Failure, string(changes), sourceContext)
		return system, user, nil

	default:
		return "", "", fmt.Errorf("coder: unexpected payload type %T", msg.Payload)
	}
}

// buildSourceContext reads files from disk and formats them for the LLM prompt.
func (a *CoderAgent) buildSourceContext(files []string) string {
	if len(files) == 0 {
		// Fallback to raw coder output if no file list available.
		raw, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
		return string(raw)
	}
	var sb strings.Builder
	for _, path := range files {
		content, err := os.ReadFile(filepath.Join(a.root, path))
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(&sb, "**%s**\n```\n%s\n```\n\n", path, string(content))
	}
	return sb.String()
}

// collectExistingSourceFiles walks the project root and returns relative paths
// of existing source files. Used to provide brownfield context to the coder.
func (a *CoderAgent) collectExistingSourceFiles() []string {
	var files []string
	_ = filepath.Walk(a.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(a.root, path)
		if rel == "" || strings.HasPrefix(rel, ".") {
			return nil
		}
		if isExistingSourceFile(rel) {
			files = append(files, rel)
		}
		return nil
	})
	return filterSourceFiles(files)
}

// isExistingSourceFile returns true for files that contain application source code.
func isExistingSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	sourceExts := map[string]bool{
		".go": true, ".rs": true, ".py": true, ".js": true, ".ts": true,
		".tsx": true, ".jsx": true, ".java": true, ".rb": true,
	}
	return sourceExts[ext]
}

// BuildAndFix compiles the project and iteratively fixes build errors.
// When MaxFixAttempts is 0 (default), it keeps retrying until the build succeeds.
// Returns the updated file list after all fixes.
func (a *CoderAgent) BuildAndFix(ctx context.Context, files []string) ([]string, error) {
	// Ensure go.mod exists for Go projects.
	a.ensureGoMod()

	buildCommand := a.buildCmd()
	if buildCommand == "" {
		return files, nil
	}

	maxAttempts := a.cfg.Project.MaxFixAttempts
	unlimited := maxAttempts <= 0

	for attempt := 1; unlimited || attempt <= maxAttempts; attempt++ {
		if unlimited {
			a.emitToken(fmt.Sprintf("build check (%d): $ %s\n", attempt, buildCommand), false)
		} else {
			a.emitToken(fmt.Sprintf("build check (%d/%d): $ %s\n", attempt, maxAttempts, buildCommand), false)
		}

		buildErr := a.runBuild(buildCommand)
		if buildErr == "" {
			a.emitToken("build: OK\n", false)
			return files, nil
		}

		a.emitToken(fmt.Sprintf("build failed:\n%s\n", buildErr), false)

		if !unlimited && attempt == maxAttempts {
			break
		}

		a.emitToken(fmt.Sprintf("fixing build errors (attempt %d)…\n", attempt), false)

		sourceContext := a.buildSourceContext(files)
		fixPrompt := fmt.Sprintf("Build command failed:\n$ %s\n\nBuild errors:\n%s\n\nCurrent source files:\n%s\n\nFix ALL compilation errors and output the corrected files.",
			buildCommand, buildErr, sourceContext)

		ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
			SystemPrompt: prompts.MustLoad("coder-build-fix"),
			Skills:       a.skills,
			Model:        a.model,
			Messages:     []runner.ConvMessage{{Role: "user", Content: fixPrompt}},
		})
		if err != nil {
			return files, fmt.Errorf("build fix runner: %w", err)
		}

		fixWritten, _, err := a.streamAndWriteFiles(ch)
		if err != nil {
			return files, fmt.Errorf("build fix stream: %w", err)
		}

		files = mergeFiles(files, fixWritten)
	}

	return files, fmt.Errorf("project still does not compile after %d attempts", maxAttempts)
}

// runBuild executes the build command and returns error output (empty string on success).
func (a *CoderAgent) runBuild(command string) string {
	res := a.exec.Run(command)
	if res.ExitCode != 0 {
		output := res.Stderr
		if output == "" {
			output = res.Stdout
		}
		if output == "" {
			output = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		return output
	}
	return ""
}

// ensureGoMod initialises go.mod if the project is Go and no go.mod exists.
// Uses ModulePath from the project config.
func (a *CoderAgent) ensureGoMod() {
	lang := a.cfg.Project.Language
	if lang == "" {
		lang = a.detectLanguage()
	}
	if lang != "go" && lang != "" {
		return
	}

	goModPath := filepath.Join(a.root, "go.mod")
	if fileExists(goModPath) {
		return
	}

	modulePath := a.cfg.Project.ModulePath
	if modulePath == "" {
		return
	}

	if !isValidModulePath(modulePath) {
		a.emitToken(fmt.Sprintf("skipping go mod init: invalid module path %q\n", modulePath), false)
		return
	}

	a.emitToken(fmt.Sprintf("$ go mod init %s\n", modulePath), false)
	res := a.exec.RunUnchecked(fmt.Sprintf("go mod init %s", modulePath))
	if res.ExitCode != 0 {
		output := res.Stderr
		if output == "" {
			output = res.Stdout
		}
		a.emitToken(fmt.Sprintf("go mod init failed: %s\n", output), false)
	}
}

// buildCmd returns the build command for the project language.
func (a *CoderAgent) buildCmd() string {
	lang := a.cfg.Project.Language
	if lang == "" {
		lang = a.detectLanguage()
	}
	switch lang {
	case "go":
		return "go build ./..."
	case "rust":
		return "cargo build"
	case "node", "javascript", "typescript":
		return "npm run build --if-present"
	case "java":
		if fileExists(filepath.Join(a.root, "pom.xml")) {
			return "mvn compile -q"
		}
		return "gradle compileJava -q"
	default:
		return ""
	}
}

// detectLanguage determines the project language from project files.
func (a *CoderAgent) detectLanguage() string {
	markers := map[string]string{
		"go.mod":           "go",
		"Cargo.toml":       "rust",
		"package.json":     "node",
		"pom.xml":          "java",
		"build.gradle":     "java",
		"requirements.txt": "python",
		"pyproject.toml":   "python",
	}
	for file, lang := range markers {
		if fileExists(filepath.Join(a.root, file)) {
			return lang
		}
	}
	return ""
}

// filterSourceFiles returns only source code files, excluding docs, configs,
// binaries, and non-essential files that would bloat the LLM context.
func filterSourceFiles(files []string) []string {
	var filtered []string
	for _, f := range files {
		lower := strings.ToLower(f)
		// Skip non-source files.
		switch {
		case strings.HasPrefix(f, "docs/"),
			strings.HasPrefix(f, "doc/"),
			strings.HasPrefix(f, "."):
			continue
		case strings.HasSuffix(lower, ".md"),
			strings.HasSuffix(lower, ".txt"),
			strings.HasSuffix(lower, ".yaml"),
			strings.HasSuffix(lower, ".yml"),
			strings.HasSuffix(lower, ".json") && !strings.Contains(f, "package.json"),
			strings.HasSuffix(lower, ".toml") && !strings.Contains(f, "cargo.toml"),
			strings.HasSuffix(lower, ".lock"),
			strings.HasSuffix(lower, ".sum"),
			strings.HasSuffix(lower, ".mod"):
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// mergeFiles returns a combined file list without duplicates.
func mergeFiles(existing, added []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		seen[f] = true
	}
	result := make([]string, len(existing))
	copy(result, existing)
	for _, f := range added {
		if !seen[f] {
			result = append(result, f)
			seen[f] = true
		}
	}
	return result
}

// FileBlock is a single file extracted from LLM output.
type FileBlock struct {
	Path    string
	Content string
}

// ExtractFileBlocks parses LLM output for code blocks with associated file paths.
func ExtractFileBlocks(output string) []FileBlock {
	lines := strings.Split(output, "\n")
	var blocks []FileBlock
	var contentLines []string
	inFence := false
	lastPath := ""

	isFenceLine := func(trimmed string) bool {
		return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
	}

	fenceTag := func(trimmed string) string {
		if strings.HasPrefix(trimmed, "```") {
			return strings.TrimPrefix(trimmed, "```")
		}
		return strings.TrimPrefix(trimmed, "~~~")
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if isFenceLine(trimmed) {
			if inFence {
				if lastPath == "" {
					lastPath = guessFilePathFromContent(contentLines)
				}
				if lastPath != "" {
					blocks = append(blocks, FileBlock{
						Path:    lastPath,
						Content: strings.Join(contentLines, "\n") + "\n",
					})
				}
				inFence = false
				contentLines = nil
				lastPath = ""
				continue
			}
			lastPath = findPathBefore(lines, i)
			if lastPath == "" {
				lastPath = pathFromFenceTag(fenceTag(trimmed))
			}
			inFence = true
			contentLines = nil
			continue
		}

		if inFence {
			contentLines = append(contentLines, line)
		}
	}

	return blocks
}

// findPathBefore scans up to 3 lines above idx for a file path.
func findPathBefore(lines []string, idx int) string {
	for i := idx - 1; i >= 0 && i >= idx-5; i-- {
		trim := strings.TrimSpace(lines[i])
		if trim == "" {
			continue
		}
		if p := parseFilePath(trim); p != "" {
			return p
		}
	}
	return ""
}

// parseFilePath extracts a file path from a line of text.
func parseFilePath(line string) string {
	cleaned := line
	cleaned = strings.TrimSpace(cleaned)

	// Strip numbered list markers: "1. ", "5. ", "1) "
	for i, c := range cleaned {
		if c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == ')') && i+1 < len(cleaned) && cleaned[i+1] == ' ' {
			cleaned = strings.TrimSpace(cleaned[i+2:])
			break
		}
		break
	}

	// Strip bullets, heading markers, and common emoji/decorations.
	cleaned = strings.TrimLeft(cleaned, "#*-•→▶📁📄🔧⚙️ \t")
	cleaned = strings.TrimSpace(cleaned)

	for _, prefix := range []string{
		"File:", "file:", "Filename:", "filename:",
		"Create file", "Create", "create file", "create",
		"Update file", "Update", "update file", "update",
		"Path:", "path:",
	} {
		cleaned = strings.TrimPrefix(cleaned, prefix)
	}
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.Trim(cleaned, "`*\"'")
	cleaned = strings.TrimSpace(cleaned)

	// Strip wrapping parentheses: (path/to/file.ext)
	if strings.HasPrefix(cleaned, "(") && strings.HasSuffix(cleaned, ")") {
		cleaned = cleaned[1 : len(cleaned)-1]
		cleaned = strings.TrimSpace(cleaned)
	}

	// Strip wrapping square brackets: [path/to/file.ext]
	if strings.HasPrefix(cleaned, "[") && strings.HasSuffix(cleaned, "]") {
		cleaned = cleaned[1 : len(cleaned)-1]
		cleaned = strings.TrimSpace(cleaned)
	}

	cleaned = strings.Trim(cleaned, "`*\"'")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimSuffix(cleaned, ":")
	cleaned = strings.TrimSuffix(cleaned, " -")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" || strings.ContainsAny(cleaned, " \t{}()[]<>") {
		return ""
	}
	if !strings.Contains(cleaned, ".") {
		return ""
	}
	cleaned = strings.TrimPrefix(cleaned, "./")

	if strings.Contains(cleaned, "..") {
		return ""
	}

	return sanitizeFilePath(cleaned, "")
}

// sanitizeFilePath cleans a file path produced by an LLM.
// It strips absolute path prefixes (like /Users/x/project/) and removes
// the project root from paths that accidentally embed it.
func sanitizeFilePath(path, root string) string {
	path = strings.TrimSpace(path)
	path = filepath.ToSlash(path)

	// Strip leading slash to make relative.
	path = strings.TrimPrefix(path, "/")

	// If the path starts with a home directory pattern, strip everything
	// up to a recognizable project structure marker.
	if looksLikeAbsolutePrefix(path) {
		path = stripToProjectRelative(path)
	}

	// If root is provided and present in the path, strip it.
	if root != "" {
		rootSlash := filepath.ToSlash(root)
		rootSlash = strings.TrimPrefix(rootSlash, "/")
		if idx := strings.Index(path, rootSlash); idx >= 0 {
			path = path[idx+len(rootSlash):]
			path = strings.TrimPrefix(path, "/")
		}
	}

	path = strings.TrimPrefix(path, "./")
	if path == "" || strings.Contains(path, "..") {
		return ""
	}
	return path
}

// looksLikeAbsolutePrefix returns true if the path starts with what looks like
// a filesystem prefix (Users/, home/, etc.) rather than a project-relative path.
func looksLikeAbsolutePrefix(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range []string{"users/", "home/", "tmp/", "var/", "opt/", "root/"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// stripToProjectRelative removes absolute path components, keeping only the
// project-relative part. It looks for common Go project structure markers.
func stripToProjectRelative(path string) string {
	markers := []string{"/cmd/", "/internal/", "/pkg/", "/src/", "/config/", "/configs/", "/api/", "/web/", "/test/"}
	for _, marker := range markers {
		if idx := strings.Index(path, marker); idx >= 0 {
			return path[idx+1:] // strip everything before the marker, keep "cmd/..."
		}
	}

	// Fallback: find the last segment that looks like a project root
	// by finding a directory that contains source-like structure.
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.Join(parts[i:], "/")
		if strings.Contains(candidate, "/") && strings.Contains(candidate, ".") {
			// Check if this looks like a relative project path.
			lower := strings.ToLower(parts[i])
			if lower == "cmd" || lower == "internal" || lower == "pkg" || lower == "src" ||
				lower == "config" || lower == "api" || lower == "web" || lower == "test" {
				return candidate
			}
		}
	}

	return path
}

// pathFromFenceTag extracts a file path from a code fence tag.
// Handles patterns like: ```go src/main.go, ```tsx title="src/App.tsx",
// ```file=src/main.go, etc.
func pathFromFenceTag(tag string) string {
	trimmed := strings.TrimSpace(tag)

	// Handle key=value attributes: title="path", file="path", file=path
	for _, attr := range []string{"title=", "file=", "path=", "name="} {
		if idx := strings.Index(strings.ToLower(trimmed), attr); idx >= 0 {
			val := trimmed[idx+len(attr):]
			val = strings.Trim(val, "\"' ")
			val = strings.TrimPrefix(val, "./")
			if strings.Contains(val, ".") && !strings.ContainsAny(val, " \t{}()[]<>") && !strings.Contains(val, "..") {
				return val
			}
		}
	}

	for _, part := range strings.Fields(trimmed) {
		if strings.Contains(part, ".") && !strings.ContainsAny(part, " \t{}()[]<>") {
			p := strings.TrimPrefix(part, "./")
			if !strings.Contains(p, "..") {
				return p
			}
		}
	}
	return ""
}

// guessFilePathFromContent checks for path hints in code comments, then falls
// back to inferring from language-specific patterns (Go, HTML, CSS, JS/TS, etc.).
func guessFilePathFromContent(lines []string) string {
	// First pass: look for explicit path comments.
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		commentPrefixes := []string{"//", "#", "/*", "<!--", "{/*", "<%--"}
		isComment := false
		comment := ""
		for _, cp := range commentPrefixes {
			if strings.HasPrefix(trim, cp) {
				isComment = true
				comment = strings.TrimLeft(strings.TrimPrefix(trim, cp), " ")
				// Strip closing comment markers.
				for _, suffix := range []string{"*/", "-->", "*/}", "--%>"} {
					comment = strings.TrimSuffix(comment, suffix)
				}
				comment = strings.TrimSpace(comment)
				break
			}
		}
		if !isComment {
			continue
		}
		for _, prefix := range []string{"file:", "File:", "filename:", "Filename:", "path:", "Path:"} {
			if strings.HasPrefix(comment, prefix) {
				candidate := strings.TrimSpace(strings.TrimPrefix(comment, prefix))
				if strings.Contains(candidate, ".") && !strings.ContainsAny(candidate, " \t") && !strings.Contains(candidate, "..") {
					return strings.TrimPrefix(candidate, "./")
				}
			}
		}
		if strings.Contains(comment, ".") && strings.Contains(comment, "/") &&
			!strings.ContainsAny(comment, " \t") && !strings.Contains(comment, "..") {
			return strings.TrimPrefix(comment, "./")
		}
	}

	joined := strings.Join(lines, "\n")

	// Detect HTML files.
	if strings.Contains(joined, "<!DOCTYPE") || strings.Contains(joined, "<html") {
		return "index.html"
	}

	// Detect Vue single-file components.
	if strings.Contains(joined, "<template>") && strings.Contains(joined, "<script") {
		return "src/App.vue"
	}

	// Detect Svelte components.
	if strings.Contains(joined, "<script") && strings.Contains(joined, "<style") && strings.Contains(joined, "{#") {
		return "src/App.svelte"
	}

	// Infer from Go package declaration.
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "package ") {
			pkg := strings.TrimSpace(strings.TrimPrefix(trim, "package"))
			if pkg == "" {
				continue
			}
			if pkg == "main" {
				for _, l := range lines {
					if strings.TrimSpace(l) == "func main() {" {
						return "cmd/app/main.go"
					}
				}
				return "cmd/app/main.go"
			}
			for _, l := range lines {
				if strings.Contains(l, "func Test") {
					return "internal/" + pkg + "/" + pkg + "_test.go"
				}
			}
			return "internal/" + pkg + "/" + pkg + ".go"
		}
	}

	// Detect CSS/SCSS files.
	isCSSLike := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "@import") || strings.HasPrefix(trim, "@media") ||
			strings.HasPrefix(trim, "@keyframes") || strings.HasPrefix(trim, "@font-face") ||
			strings.HasPrefix(trim, ":root") || strings.HasPrefix(trim, "body {") ||
			strings.HasPrefix(trim, "html {") || strings.HasPrefix(trim, "* {") {
			isCSSLike = true
			break
		}
	}
	if isCSSLike {
		if strings.Contains(joined, "$") || strings.Contains(joined, "@mixin") {
			return "src/styles.scss"
		}
		return "src/styles.css"
	}

	// Detect JS/TS/React files from import patterns.
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "import ") && strings.Contains(trim, "from ") {
			if strings.Contains(trim, "react") || strings.Contains(trim, "React") {
				// Check for default export to name the component.
				for _, l := range lines {
					t := strings.TrimSpace(l)
					if strings.HasPrefix(t, "export default function ") {
						name := strings.TrimPrefix(t, "export default function ")
						if idx := strings.IndexAny(name, "( {"); idx > 0 {
							name = name[:idx]
						}
						if name != "" {
							return "src/components/" + name + ".tsx"
						}
					}
					if strings.HasPrefix(t, "export default ") && !strings.HasPrefix(t, "export default function") {
						return "src/App.tsx"
					}
				}
				return "src/App.tsx"
			}
			// Generic JS/TS module.
			if strings.Contains(joined, ": ") && (strings.Contains(joined, "interface ") || strings.Contains(joined, "type ")) {
				return "src/index.ts"
			}
			return "src/index.js"
		}
	}

	// Detect Python files.
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "def ") || strings.HasPrefix(trim, "class ") ||
			(strings.HasPrefix(trim, "import ") && !strings.Contains(trim, "from ")) ||
			strings.HasPrefix(trim, "from ") {
			if strings.Contains(joined, "if __name__") {
				return "main.py"
			}
			return "app.py"
		}
	}

	// Detect Rust files.
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "fn main()") {
			return "src/main.rs"
		}
		if strings.HasPrefix(trim, "use ") || strings.HasPrefix(trim, "mod ") ||
			strings.HasPrefix(trim, "pub fn ") || strings.HasPrefix(trim, "pub struct ") {
			return "src/lib.rs"
		}
	}

	// Detect JSON (package.json, tsconfig, etc.).
	trimmedJoined := strings.TrimSpace(joined)
	if strings.HasPrefix(trimmedJoined, "{") {
		if strings.Contains(joined, "\"dependencies\"") || strings.Contains(joined, "\"name\"") {
			return "package.json"
		}
		if strings.Contains(joined, "\"compilerOptions\"") {
			return "tsconfig.json"
		}
	}

	return ""
}

// isValidModulePath checks that a Go module path contains only safe characters
// and cannot be used for shell injection when passed to `go mod init`.
func isValidModulePath(path string) bool {
	if path == "" || len(path) > 256 {
		return false
	}
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '/' || r == '-' || r == '_' || r == '~':
		default:
			return false
		}
	}
	return !strings.Contains(path, "..")
}
