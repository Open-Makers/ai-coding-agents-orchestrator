package agent

import (
	"context"
	"errors"
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
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
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
	// Targets maps file -> symbol names for ADDITIVE scoped related context.
	// Files here that are not already in Files are appended, rendered scoped.
	// Never removes Files from context. Set by the runner when enabled.
	Targets map[string][]string
}

// CoderResult holds the list of files written by the coder.
type CoderResult struct {
	Files []string
}

// CoderResearchPayload requests a read-only codebase review for the brown and
// fix pipelines. The coder analyses existing code against the requirements and
// returns prose findings; it writes no files.
type CoderResearchPayload struct {
	Requirements   string
	ProjectContext string
}

// BuildFixStuckError indicates that the build/test fix loop is repeating the
// same failure and was aborted to avoid an infinite cycle.
type BuildFixStuckError struct {
	RepeatCount int
}

func (e BuildFixStuckError) Error() string {
	return fmt.Sprintf("build fix stuck: same error repeated %d times", e.RepeatCount)
}

// IsBuildFixStuck reports whether err represents an aborted fix loop caused by
// repeated identical validation failures.
func IsBuildFixStuck(err error) bool {
	var target BuildFixStuckError
	return errors.As(err, &target)
}

// CoderAgent generates code and writes files to disk one by one.
type CoderAgent struct {
	BaseAgent
	runner      runner.LLMRunner
	fixerRunner runner.LLMRunner // optional: separate runner for fix iterations
	ws          artifacts.Workspace
	root        string
	cfg         config.Config
	exec        *executil.Runner
	skills      []string
	model       string
	fixerModel  string
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

// SetFixerRunner configures an optional separate runner/model used for fix
// iterations (BuildAndFix, CoderFixPayload). When not set, fixes use the
// primary runner/model.
func (a *CoderAgent) SetFixerRunner(r runner.LLMRunner, model string) {
	a.fixerRunner = r
	a.fixerModel = model
}

// fixRunner returns the runner to use for fix completions.
func (a *CoderAgent) fixRunner() runner.LLMRunner {
	if a.fixerRunner != nil {
		return a.fixerRunner
	}
	return a.runner
}

// fixModel returns the model to use for fix completions.
func (a *CoderAgent) fixModel() string {
	if a.fixerModel != "" {
		return a.fixerModel
	}
	return a.model
}

func (a *CoderAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	systemPrompt, userContent, err := a.buildPrompt(msg)
	if err != nil {
		return bus.Message{}, err
	}

	// Use fixer runner/model for fix payloads, primary for initial generation.
	r, mdl := a.runner, a.model
	if _, isFix := msg.Payload.(CoderFixPayload); isFix {
		r, mdl = a.fixRunner(), a.fixModel()
	}

	ch, err := r.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        mdl,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("coder: runner: %w", err)
	}

	written, fullOutput, usage, err := a.streamAndWriteFiles(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("coder: stream: %w", err)
	}
	totalUsage := usage

	// Always save raw output for debugging, even if no files were extracted.
	if err := a.ws.WriteFile(artifacts.RawCoderOutputFile, []byte(fullOutput+"\n")); err != nil {
		return bus.Message{}, err
	}

	sections := parseSections(fullOutput, "CHANGES", "TEST_CMDS")

	if err := a.ws.WriteFile(artifacts.ChangesFile, []byte(sections["CHANGES"]+"\n")); err != nil {
		return bus.Message{}, err
	}
	if cmds := strings.TrimSpace(sections["TEST_CMDS"]); cmds != "" && !a.ws.FileExists(artifacts.TestCmdsFile) {
		if err := a.ws.WriteFile(artifacts.TestCmdsFile, []byte(cmds+"\n")); err != nil {
			return bus.Message{}, err
		}
	}

	// Initial code generation must produce at least one file.
	// Two failure modes are recovered with different retry prompts:
	//   1. Model emitted source code but skipped file-block markers → reformat retry.
	//   2. Model claimed "no changes required" without implementing the sub-task
	//      (common hallucination: it runs `go build && go test`, sees green, and
	//      concludes the work is done — but green tests don't prove the requested
	//      feature exists) → push back and demand actual implementation.
	if _, isInitial := msg.Payload.(CoderPayload); isInitial && len(written) == 0 {
		if isNoChangesDeclared(sections["CHANGES"]) {
			written, totalUsage, err = a.retryDemandImplementation(ctx, r, mdl, systemPrompt, userContent, totalUsage)
		} else {
			written, totalUsage, err = a.retryFormatCorrection(ctx, r, mdl, systemPrompt, fullOutput, totalUsage)
		}
		if err != nil {
			return bus.Message{}, fmt.Errorf("coder: retry: %w", err)
		}
		if len(written) == 0 {
			return bus.Message{}, fmt.Errorf("coder: no file blocks found in initial code generation output (raw output saved to %s)", artifacts.RawCoderOutputFile)
		}
	}

	// Ensure go.mod exists immediately after writing files so that
	// subsequent steps (tester, go mod tidy) can find it.
	a.ensureGoMod()

	a.emitUsage(totalUsage)
	a.Bus.Publish(bus.NewMessage(bus.RoleCoder, "", bus.MsgEvent, "EventFilesWritten"))

	return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, CoderResult{Files: written}), nil
}

// Research performs a read-only review of the existing codebase against the
// user's requirements and returns the coder's analysis as text. It writes no
// files; it is used as Phase 0 of the brown and fix pipelines.
func (a *CoderAgent) Research(ctx context.Context, p CoderResearchPayload) (string, error) {
	systemPrompt := fmt.Sprintf(prompts.MustLoad("coder-research"), p.ProjectContext)

	var sb strings.Builder
	sb.WriteString("Requirements from the user:\n\n")
	sb.WriteString(strings.TrimSpace(p.Requirements))
	if src := a.buildSourceContext(a.collectExistingSourceFiles()); strings.TrimSpace(src) != "" {
		sb.WriteString("\n\nExisting source code:\n\n")
		sb.WriteString(src)
	}

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: sb.String()}},
	})
	if err != nil {
		return "", fmt.Errorf("coder research: runner: %w", err)
	}

	var out strings.Builder
	var usage runner.TokenUsage
	for tok := range ch {
		if tok.Error != nil {
			return "", fmt.Errorf("coder research: %w", tok.Error)
		}
		if tok.Done {
			if tok.Usage != nil {
				usage = *tok.Usage
			}
			break
		}
		if tok.Reasoning != "" {
			a.emitToken(tok.Reasoning, false)
		}
		if tok.Text == "" {
			continue
		}
		a.emitToken(tok.Text, false)
		out.WriteString(tok.Text)
	}
	a.emitUsage(usage)

	return strings.TrimSpace(out.String()), nil
}

// isNoChangesDeclared reports whether the CHANGES section explicitly states
// that no file modifications are needed (e.g. the codebase already satisfies
// the stage requirements). Used to distinguish a legitimate no-op from a
// formatting failure where the model emitted code without file blocks.
func isNoChangesDeclared(changes string) bool {
	c := strings.ToLower(strings.TrimSpace(changes))
	if c == "" {
		return false
	}
	phrases := []string{
		"no changes required",
		"no changes needed",
		"no file changes",
		"no changes necessary",
		"no modifications required",
		"no modifications needed",
	}
	for _, p := range phrases {
		if strings.Contains(c, p) {
			return true
		}
	}
	return false
}

// retryFormatCorrection sends the raw output back to the model and asks it to
// reformat any source code using proper file-block syntax. Used when the first
// pass produces text/code without the required bold-path + fence structure.
func (a *CoderAgent) retryFormatCorrection(ctx context.Context, r runner.LLMRunner, mdl, systemPrompt, rawOutput string, prevUsage runner.TokenUsage) ([]string, runner.TokenUsage, error) {
	a.emitToken("\n[retrying: reformatting output into file blocks…]\n", false)

	correctionPrompt := "The following is source code you produced, but it is missing the required file block format.\n" +
		"Reformat ALL source code below into proper file blocks — one block per file.\n\n" +
		"Required format for EVERY file:\n\n" +
		"**path/to/file.go**\n```go\n// full file content\n```\n\n" +
		"Do NOT add explanations. Output ONLY the file blocks.\n\n" +
		"Source code to reformat:\n\n" + rawOutput

	ch, err := r.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Model:        mdl,
		Messages:     []runner.ConvMessage{{Role: "user", Content: correctionPrompt}},
	})
	if err != nil {
		return nil, prevUsage, err
	}

	written, retryOutput, retryUsage, err := a.streamAndWriteFiles(ch)
	if err != nil {
		return nil, prevUsage, err
	}
	_ = a.ws.WriteFile(artifacts.RawCoderOutputFile, []byte(retryOutput+"\n"))

	total := runner.TokenUsage{
		InputTokens:  prevUsage.InputTokens + retryUsage.InputTokens,
		OutputTokens: prevUsage.OutputTokens + retryUsage.OutputTokens,
	}
	return written, total, nil
}

// retryDemandImplementation re-prompts the model after it claimed no changes
// were needed. The original user prompt is replayed with a prefix that rejects
// the "tests pass, nothing to do" shortcut: a green `go build && go test` does
// not prove the requested feature exists — only file blocks do.
func (a *CoderAgent) retryDemandImplementation(ctx context.Context, r runner.LLMRunner, mdl, systemPrompt, originalUserContent string, prevUsage runner.TokenUsage) ([]string, runner.TokenUsage, error) {
	a.emitToken("\n[retrying: coder claimed no changes needed — demanding actual implementation]\n", false)

	pushback := "You previously responded with \"no changes required\" without writing any files. " +
		"That answer is REJECTED. A passing `go build` / `go test` does NOT prove the requested " +
		"feature is implemented — it only proves the existing code compiles. You MUST produce " +
		"file blocks that implement the work described below.\n\n" +
		"If a file already exists and needs no edits, do not emit it. But you MUST emit at least " +
		"one file block containing the source code that satisfies the task. Do NOT respond with " +
		"explanations only. Do NOT claim the work is already done.\n\n" +
		"Original task:\n\n" + originalUserContent

	ch, err := r.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        mdl,
		Messages:     []runner.ConvMessage{{Role: "user", Content: pushback}},
	})
	if err != nil {
		return nil, prevUsage, err
	}

	written, retryOutput, retryUsage, err := a.streamAndWriteFiles(ch)
	if err != nil {
		return nil, prevUsage, err
	}
	_ = a.ws.WriteFile(artifacts.RawCoderOutputFile, []byte(retryOutput+"\n"))

	total := runner.TokenUsage{
		InputTokens:  prevUsage.InputTokens + retryUsage.InputTokens,
		OutputTokens: prevUsage.OutputTokens + retryUsage.OutputTokens,
	}
	return written, total, nil
}

// streamAndWriteFiles consumes the LLM token stream, detects code fence blocks,
// and writes each file to disk immediately when a fence closes.
// It also captures and returns the TokenUsage from the Done token.
func (a *CoderAgent) streamAndWriteFiles(ch <-chan runner.Token) ([]string, string, runner.TokenUsage, error) {
	var fullOutput strings.Builder
	var lineBuf strings.Builder
	var recentLines []string // sliding window for path detection
	var contentLines []string
	var written []string
	var capturedUsage runner.TokenUsage

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
						if errors.Is(err, errNoOpRewrite) {
							// A no-op rewrite means the model emitted a valid
							// file block whose content happens to be identical
							// to what's already on disk. The block was still
							// correctly recognised — count it so the empty-
							// output retry path is not triggered (otherwise
							// the model retries, produces the same identical
							// content, and the pipeline aborts with
							// "no file blocks found").
							written = append(written, currentPath)
							a.emitToken(fmt.Sprintf("skipped no-op rewrite: %s\n", currentPath), false)
						} else {
							a.emitToken(fmt.Sprintf("error writing %s: %v\n", currentPath, err), false)
						}
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
			return written, fullOutput.String(), capturedUsage, tok.Error
		}
		if tok.Done {
			if tok.Usage != nil {
				capturedUsage = *tok.Usage
			}
			break
		}
		// Reasoning is display-only (heartbeats, tool-use notices). Show it so
		// the user sees activity during fixing, but never accumulate it into
		// the parsed file output.
		if tok.Reasoning != "" {
			a.emitToken(tok.Reasoning, false)
		}
		if tok.Text == "" {
			continue
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
	return written, fullOutput.String(), capturedUsage, nil
}

// writeOneFile writes a single file to disk under the project root.
// Returns ("", nil) when the new content is byte-identical to what is already
// on disk — that is treated as a no-op rewrite and silently skipped to avoid
// inflating the "files changed" list during fix iterations.
func (a *CoderAgent) writeOneFile(path, content string) error {
	path = sanitizeFilePath(path, a.root)
	if path == "" {
		return fmt.Errorf("empty path after sanitization")
	}
	if strings.HasSuffix(path, "_test.go") {
		return fmt.Errorf("refusing to write %s: tester owns test files", path)
	}
	if strings.HasSuffix(path, ".go") {
		content = fixInvalidGoPackage(content)
	}
	target := filepath.Join(a.root, path)
	if existing, err := os.ReadFile(target); err == nil && string(existing) == content { // #nosec G304 -- target is sanitized via filepath.Join under the project root
		return errNoOpRewrite
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return fmt.Errorf("mkdir for %s: %w", path, err)
	}
	return os.WriteFile(target, []byte(content), 0o600)
}

// errNoOpRewrite signals that a file write was suppressed because the content
// was identical to the existing file. Used to short-circuit reporting in the
// streaming writer without surfacing it as a real error to the user.
var errNoOpRewrite = errors.New("no-op rewrite skipped")

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
			if v := detectGoToolchain(); v != "" {
				moduleInfo += fmt.Sprintf("The locally installed Go toolchain is %s. Target this exact version — do NOT use older Go versions or invent version numbers.\n", v)
			}
		}

		projectName := a.cfg.Project.Name
		if projectName == "" {
			projectName = filepath.Base(a.root)
		}

		system := fmt.Sprintf(prompts.MustLoad("coder-initial"), moduleInfo, projectName, projectName, payload.ProjectContext)

		const fileBlockReminder = `OUTPUT FORMAT — you MUST follow this exactly for every file:

**path/to/file.go**
` + "```" + `go
package example

// complete file content here
` + "```" + `

Every code block MUST be preceded by the bold file path on its own line.
Without it the file will NOT be saved. No descriptions, no prose — only file blocks.`

		var userParts []string

		if payload.StageIndex > 0 {
			userParts = append(userParts, fmt.Sprintf(
				"You are implementing %s.\nImplement ONLY this stage's scope. Do NOT implement features from other stages.\n\n%s",
				payload.StageName, fileBlockReminder))
		} else {
			userParts = append(userParts, "Implement the following plan.\nIMPORTANT: Implement ALL features marked as \"Must Have\" and \"Should Have\". Skip \"Could Have\" and \"Won't Have\" items.\n\n"+fileBlockReminder)
		}

		userParts = append(userParts, fmt.Sprintf("Plan:\n%s", payload.Plan))

		// Include existing project files for brownfield context.
		// Split test files from source files so TDD tests are never mislabeled
		// as "files to modify" (which causes models to describe patches instead
		// of outputting new source code blocks).
		existingFiles := a.collectExistingSourceFiles()
		var existingTests, existingSrc []string
		for _, f := range existingFiles {
			if strings.HasSuffix(f, "_test.go") {
				existingTests = append(existingTests, f)
			} else {
				existingSrc = append(existingSrc, f)
			}
		}
		if len(existingTests) > 0 {
			testCtx := a.buildSourceContext(existingTests)
			if testCtx != "" {
				userParts = append(userParts, fmt.Sprintf(
					"EXISTING TEST FILES (TDD) — implement source code to make these tests pass. Do NOT re-output or modify these test files:\n%s",
					testCtx))
			}
		}
		if len(existingSrc) > 0 {
			sourceCtx := a.buildSourceContext(existingSrc)
			if sourceCtx != "" {
				userParts = append(userParts, fmt.Sprintf(
					"EXISTING PROJECT FILES — you MUST modify these files, not create new ones with different paths:\n%s",
					sourceCtx))
			}
		}

		if len(payload.PriorFiles) > 0 {
			sourceCtx := a.buildSourceContext(FilterSourceFiles(payload.PriorFiles))
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

		// Extract files mentioned in the failure for targeted fixing.
		// They drive both the prompt hint and the seed-based context expansion
		// (their imports + callers get pulled into the source context).
		errorFiles := extractFilesFromErrors(payload.Failure)

		sourceContext := a.buildSourceContextWithSeeds(payload.Files, errorFiles)

		// Additive scoped related context: append semantically-related decls
		// from files not already included, rendered scoped (cheap). Never
		// removes the changed/error files above.
		if related := a.buildScopedRelatedContext(payload.Files, errorFiles, payload.Targets); related != "" {
			sourceContext += "\n\nRelated context (scoped to relevant declarations):\n" + related
		}

		errorFileList := ""
		if len(errorFiles) > 0 {
			errorFileList = "\n\nFiles referenced in errors:\n- " + strings.Join(errorFiles, "\n- ")
		}

		system := fmt.Sprintf(prompts.MustLoad("coder-fix"), payload.ProjectContext)

		user := fmt.Sprintf("Error output:\n%s%s\n\nPrevious changes summary:\n%s\n\nCurrent source files:\n%s\n\nFix ONLY the files that have errors. Do NOT re-output unchanged files. Do NOT create new files or restructure the project.",
			payload.Failure, errorFileList, string(changes), sourceContext)
		return system, user, nil

	default:
		return "", "", fmt.Errorf("coder: unexpected payload type %T", msg.Payload)
	}
}

// maxCoderSourceContext caps total source context size sent to the coder during fixes.
const maxCoderSourceContext = 60000

// maxCoderPerFileSize caps per-file size when rendering coder source context.
// Larger than the review per-file budget — coders need full visibility into
// the files they may modify.
const maxCoderPerFileSize = 8000

// buildSourceContext reads files from disk and formats them for the LLM prompt.
// Applies size limits to prevent unbounded context growth in large projects.
// Falls back to RawCoderOutputFile when no files are known (initial generation).
func (a *CoderAgent) buildSourceContext(files []string) string {
	return a.buildSourceContextWithSeeds(files, nil)
}

// buildSourceContextWithSeeds is the seed-aware variant used by fix paths.
// Seeds (e.g. files extracted from compiler/test errors) get prepended along
// with their imports and callers of their primary exported symbol.
func (a *CoderAgent) buildSourceContextWithSeeds(files, seeds []string) string {
	if len(files) == 0 && len(seeds) == 0 {
		// Fallback to raw coder output when nothing else is available
		// (used during initial generation before any files are written).
		raw, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
		return string(raw)
	}
	return buildSourceContextSized(string(a.Role()), a.root, files, seeds, maxCoderSourceContext, maxCoderPerFileSize, 0)
}

// buildScopedRelatedContext renders additive, symbol-scoped context for files
// in targets that are NOT already part of the whole-rendered fix context
// (mandatory = files + seeds). Returns "" when there is nothing new to add.
// This only ADDS related declarations; it never removes the code being fixed.
func (a *CoderAgent) buildScopedRelatedContext(files, seeds []string, targets map[string][]string) string {
	if len(targets) == 0 {
		return ""
	}
	mandatory := make(map[string]struct{}, len(files)+len(seeds))
	for _, f := range append(append([]string{}, files...), seeds...) {
		mandatory[f] = struct{}{}
	}
	var relatedFiles []string
	scoped := make(map[string][]string)
	for f, syms := range targets {
		if _, ok := mandatory[f]; ok || len(syms) == 0 {
			continue
		}
		relatedFiles = append(relatedFiles, f)
		scoped[f] = syms
	}
	if len(relatedFiles) == 0 {
		return ""
	}
	return buildScopedSourceContext(string(a.Role())+"-related", a.root, relatedFiles, nil, scoped, maxCoderSourceContext, maxCoderPerFileSize, 0)
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
	return FilterSourceFiles(files)
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

// hardMaxBuildAttempts is a safety net against unbounded loops when errors keep
// shifting just enough to dodge same-error detection.
const hardMaxBuildAttempts = 50

// defaultSameErrorLimit is used when MaxFixAttempts is 0 (unlimited config).
const defaultSameErrorLimit = 5

// BuildAndFix compiles the project and iteratively fixes build/test errors.
// MaxFixAttempts is the number of attempts allowed at the SAME error before
// giving up — a different error resets the counter. This way the LLM gets the
// configured number of tries to repair each distinct problem rather than burning
// the budget across unrelated failures.
// Returns the updated file list after all fixes.
// Token usage across all fix iterations is accumulated and emitted at the end.
func (a *CoderAgent) BuildAndFix(ctx context.Context, files []string) ([]string, error) {
	// Ensure go.mod exists for Go projects.
	a.ensureGoMod()

	buildCommand := a.buildCmd()
	testCommands := a.testCmds()
	if buildCommand == "" && len(testCommands) == 0 {
		return files, nil
	}

	sameErrorLimit := a.cfg.Project.MaxFixAttempts
	if sameErrorLimit <= 0 {
		sameErrorLimit = defaultSameErrorLimit
	}

	prevBuildErr := ""
	sameErrorAttempt := 0
	var totalUsage runner.TokenUsage
	testContext := a.buildSourceContext(a.collectRelatedTestFiles(files))

	for total := 1; total <= hardMaxBuildAttempts; total++ {
		failureKind := "build"
		failureOutput := ""

		if buildCommand != "" {
			a.emitToken(fmt.Sprintf("$ %s\n", buildCommand), false)
			failureOutput = a.runBuild(buildCommand)
		}

		if failureOutput == "" && len(testCommands) > 0 {
			testFailure := a.runTestCommands(testCommands)
			if testFailure != "" {
				failureKind = "test"
				failureOutput = testFailure
			}
		}

		if failureOutput == "" {
			if buildCommand != "" {
				a.emitToken("build: OK\n", false)
			}
			if len(testCommands) > 0 {
				a.emitToken("tests: OK\n", false)
			}
			a.emit(bus.MsgEvent, "done")
			a.emitUsage(totalUsage)
			return files, nil
		}

		// Compare current failure with previous to track same-error attempts.
		normalizedErr := normalizeBuildError(failureOutput)
		if normalizedErr == prevBuildErr {
			sameErrorAttempt++
		} else {
			sameErrorAttempt = 1
			prevBuildErr = normalizedErr
		}

		a.emit(bus.MsgEvent, "coder_fixer")
		a.emitToken(fmt.Sprintf("validation check (%d/%d for this error)\n",
			sameErrorAttempt, sameErrorLimit), false)
		a.emitToken(fmt.Sprintf("%s failed:\n%s\n", failureKind, failureOutput), false)

		if sameErrorAttempt >= sameErrorLimit {
			a.emitToken(fmt.Sprintf("same %s error repeated %d times — aborting fix loop\n",
				failureKind, sameErrorAttempt), false)
			// Attempt auto-repair for known patterns before giving up.
			if autoFixed := a.autoFixKnownBuildErrors(failureOutput, files); autoFixed {
				a.emitToken("applied automatic fixes for known error patterns, retrying…\n", false)
				sameErrorAttempt = 0
				prevBuildErr = ""
				continue
			}
			a.emitUsage(totalUsage)
			return files, BuildFixStuckError{RepeatCount: sameErrorAttempt}
		}

		a.emitToken(fmt.Sprintf("fixing %s issues (attempt %d/%d)…\n",
			failureKind, sameErrorAttempt, sameErrorLimit), false)

		// Extract files mentioned in errors to focus the fix.
		errorFiles := extractFilesFromErrors(failureOutput)
		errorFileList := ""
		if len(errorFiles) > 0 {
			errorFileList = "\n\nFiles with errors:\n- " + strings.Join(errorFiles, "\n- ")
		}

		sourceContext := a.buildSourceContextWithSeeds(files, errorFiles)
		readOnlyTests := ""
		if testContext != "" {
			readOnlyTests = "\n\nRead-only test files (DO NOT MODIFY):\n" + testContext
		}
		validationSummary := a.validationSummary(buildCommand, testCommands)
		fixPrompt := fmt.Sprintf("Validation failed during %s.\n\n%s\n\nFailure output:\n%s%s\n\nCurrent source files:\n%s\n\nFix ONLY the files that have errors. Use the existing tests as the contract. Do NOT re-output files that already work. Do NOT create new files or restructure the project.",
			failureKind, validationSummary, failureOutput, errorFileList, sourceContext+readOnlyTests)

		ch, err := a.fixRunner().Complete(ctx, runner.CompletionRequest{
			SystemPrompt: prompts.MustLoad("coder-build-fix"),
			Skills:       a.skills,
			Model:        a.fixModel(),
			Messages:     []runner.ConvMessage{{Role: "user", Content: fixPrompt}},
		})
		if err != nil {
			return files, fmt.Errorf("build fix runner: %w", err)
		}

		fixWritten, _, fixUsage, err := a.streamAndWriteFiles(ch)
		if err != nil {
			return files, fmt.Errorf("build fix stream: %w", err)
		}
		totalUsage.InputTokens += fixUsage.InputTokens
		totalUsage.OutputTokens += fixUsage.OutputTokens

		files = mergeFiles(files, fixWritten)
	}

	a.emitUsage(totalUsage)
	return files, fmt.Errorf("project still does not compile after %d attempts", hardMaxBuildAttempts)
}

// extractFilesFromErrors parses build/test error output and returns unique
// file paths mentioned in error lines (e.g. "internal/game/board.go:15:3: ...").
func extractFilesFromErrors(errOutput string) []string {
	seen := make(map[string]bool)
	var files []string
	for _, line := range strings.Split(errOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Match "file.go:line:col:" or "file.go:line:" patterns.
		colonIdx := strings.Index(trimmed, ":")
		if colonIdx <= 0 {
			continue
		}
		candidate := trimmed[:colonIdx]
		// Must look like a file path with extension.
		if !strings.Contains(candidate, ".") {
			continue
		}
		// Skip common non-file prefixes.
		if strings.HasPrefix(candidate, "#") || strings.HasPrefix(candidate, "---") {
			continue
		}
		candidate = strings.TrimPrefix(candidate, "./")
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			files = append(files, candidate)
		}
	}
	return files
}

// normalizeBuildError strips line numbers and file paths to produce a
// comparable signature for detecting repeated identical errors.
func normalizeBuildError(errOutput string) string {
	var lines []string
	for _, line := range strings.Split(errOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Strip file:line:col prefix to compare only error messages.
		if idx := strings.Index(trimmed, ": "); idx > 0 {
			trimmed = trimmed[idx+2:]
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n")
}

// autoFixKnownBuildErrors applies automatic repairs for error patterns LLMs
// commonly produce but fail to self-correct (e.g. invalid Go package names
// with slashes). Returns true if any files were modified.
func (a *CoderAgent) autoFixKnownBuildErrors(buildErr string, files []string) bool {
	if !strings.Contains(buildErr, "expected ';', found '/'") {
		return false
	}

	fixed := false
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		data, err := safefile.ReadFile(a.root, rel)
		if err != nil {
			continue
		}
		original := string(data)
		repaired := fixInvalidGoPackage(original)
		if repaired != original {
			if err := safefile.WriteFile(a.root, rel, []byte(repaired), 0o600); err == nil {
				a.emitToken(fmt.Sprintf("auto-fixed invalid package declaration in %s\n", rel), false)
				fixed = true
			}
		}
	}
	return fixed
}

// fixInvalidGoPackage repairs invalid Go package declarations that contain
// slashes (e.g. "package internal/controller" → "package controller").
// LLMs — especially local models — frequently confuse the import path with
// the package name.
func fixInvalidGoPackage(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "package ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "package"))
		if !strings.Contains(rest, "/") {
			break // valid package declaration, nothing to fix
		}
		// Extract just the last path segment as the package name.
		parts := strings.Split(rest, "/")
		pkgName := parts[len(parts)-1]
		// Strip trailing comments or semicolons.
		if idx := strings.IndexAny(pkgName, " \t;"); idx >= 0 {
			pkgName = pkgName[:idx]
		}
		if pkgName == "" {
			break
		}
		lines[i] = "package " + pkgName
		break // only fix the first package declaration
	}
	return strings.Join(lines, "\n")
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

func (a *CoderAgent) runTestCommands(cmds []string) string {
	for _, cmd := range cmds {
		if isInteractiveCommand(cmd) {
			a.emitToken(fmt.Sprintf("skipping interactive test command: %s\n", cmd), false)
			continue
		}
		a.emitToken(fmt.Sprintf("$ %s\n", cmd), false)
		res := a.exec.Run(cmd)
		if res.ExitCode == 0 {
			continue
		}
		output := strings.TrimSpace(res.Stderr)
		if output == "" {
			output = strings.TrimSpace(res.Stdout)
		}
		if output == "" {
			output = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		return fmt.Sprintf("command: %s\n%s", cmd, output)
	}
	return ""
}

func (a *CoderAgent) testCmds() []string {
	data, err := a.ws.ReadFile(artifacts.TestCmdsFile)
	if err == nil {
		cmds := filterTestCommands(parseCmds(string(data)), a.buildCmd())
		if len(cmds) > 0 {
			return cmds
		}
	}
	return a.defaultTestCmds()
}

func (a *CoderAgent) defaultTestCmds() []string {
	switch a.detectLanguage() {
	case "go":
		return []string{"go test -count=1 ./..."}
	case "node", "javascript", "typescript":
		return []string{"npm test"}
	case "python":
		return []string{"python -m pytest"}
	case "rust":
		return []string{"cargo test"}
	default:
		return nil
	}
}

func (a *CoderAgent) collectRelatedTestFiles(files []string) []string {
	seen := make(map[string]bool)
	var tests []string
	for _, rel := range files {
		dir := filepath.Dir(rel)
		if dir == "." || dir == "" {
			dir = ""
		}
		absDir := a.root
		if dir != "" {
			absDir = filepath.Join(a.root, dir)
		}
		entries, err := os.ReadDir(absDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			testRel := entry.Name()
			if dir != "" {
				testRel = filepath.Join(dir, entry.Name())
			}
			testRel = filepath.ToSlash(testRel)
			if seen[testRel] {
				continue
			}
			seen[testRel] = true
			tests = append(tests, testRel)
		}
	}
	return tests
}

func filterTestCommands(cmds []string, buildCommand string) []string {
	var filtered []string
	for _, cmd := range cmds {
		if cmd == "" || cmd == buildCommand {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(cmd))
		switch {
		case strings.HasPrefix(lower, "go build "):
			continue
		case strings.HasPrefix(lower, "cargo build"):
			continue
		case strings.HasPrefix(lower, "npm run build"):
			continue
		case strings.HasPrefix(lower, "mvn compile"):
			continue
		case strings.HasPrefix(lower, "gradle compile"):
			continue
		}
		filtered = append(filtered, cmd)
	}
	return filtered
}

func (a *CoderAgent) validationSummary(buildCommand string, testCommands []string) string {
	var sections []string
	if buildCommand != "" {
		sections = append(sections, "Build command:\n$ "+buildCommand)
	}
	if len(testCommands) > 0 {
		sections = append(sections, "Test commands:\n$ "+strings.Join(testCommands, "\n$ "))
	}
	return strings.Join(sections, "\n\n")
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

// sourceExtensions are file extensions recognized as source code.
// Allowlist approach prevents binary files and other non-text files
// from being sent to the LLM as context.
var sourceExtensions = map[string]bool{
	".go": true, ".rs": true, ".py": true, ".js": true, ".ts": true,
	".tsx": true, ".jsx": true, ".java": true, ".rb": true, ".c": true,
	".cpp": true, ".cc": true, ".h": true, ".hpp": true, ".cs": true,
	".swift": true, ".kt": true, ".scala": true, ".php": true,
	".html": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".vue": true, ".svelte": true, ".sql": true, ".sh": true, ".bash": true,
	".zsh": true, ".proto": true, ".graphql": true, ".gql": true,
	".tf": true, ".hcl": true, ".ex": true, ".exs": true, ".erl": true,
	".hs": true, ".lua": true, ".r": true, ".pl": true, ".pm": true,
	".dart": true, ".zig": true, ".nim": true, ".v": true, ".ml": true,
}

// allowedConfigFiles are specific config filenames that should be included
// even though they have config extensions.
var allowedConfigFiles = map[string]bool{
	"package.json": true, "tsconfig.json": true, "cargo.toml": true,
	"pyproject.toml": true, "makefile": true, "dockerfile": true,
	"docker-compose.yml": true, "docker-compose.yaml": true,
}

// FilterSourceFiles returns only source code files, using an allowlist
// of recognized extensions. This prevents binaries and non-text files
// from corrupting LLM context.
func FilterSourceFiles(files []string) []string {
	var filtered []string
	for _, f := range files {
		// Skip hidden files and common non-source directories.
		if strings.HasPrefix(f, ".") || strings.HasPrefix(f, "docs/") || strings.HasPrefix(f, "doc/") {
			continue
		}

		ext := strings.ToLower(filepath.Ext(f))
		base := strings.ToLower(filepath.Base(f))

		// Include recognized source files.
		if sourceExtensions[ext] {
			filtered = append(filtered, f)
			continue
		}

		// Include specific config files that provide build context.
		if allowedConfigFiles[base] {
			filtered = append(filtered, f)
			continue
		}

		// Skip everything else: binaries, images, archives, lock files,
		// .md, .txt, .sum, .mod, and files without extensions.
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

	// Try extracting a backtick-quoted path from prose text first.
	// Handles patterns like: Here is `cmd/game/main.go`:
	if p := extractBacktickPath(cleaned); p != "" {
		return p
	}

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

	// If there are spaces remaining, try to extract the leading path token.
	// Handles: "cmd/game/main.go — Main entry point" or "cmd/game/main.go (state management)"
	if strings.ContainsAny(cleaned, " \t") {
		candidate := extractLeadingPath(cleaned)
		if candidate != "" {
			return candidate
		}
		return ""
	}

	if cleaned == "" || strings.ContainsAny(cleaned, "{}()[]<>") {
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

// extractBacktickPath finds a file path inside backticks within prose text.
// Handles: "Here is `cmd/game/main.go`:", "File `internal/game/state.go`:"
func extractBacktickPath(line string) string {
	start := strings.Index(line, "`")
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], "`")
	if end < 0 {
		return ""
	}
	candidate := strings.TrimSpace(line[start+1 : start+1+end])
	candidate = strings.TrimPrefix(candidate, "./")
	if candidate == "" || strings.ContainsAny(candidate, " \t{}()[]<>") {
		return ""
	}
	// Require both a dot (extension) and a slash (directory) to avoid
	// matching inline code like `fmt.Println` or `errors.New`.
	if !strings.Contains(candidate, ".") || !strings.Contains(candidate, "/") || strings.Contains(candidate, "..") {
		return ""
	}
	return sanitizeFilePath(candidate, "")
}

// extractLeadingPath takes the first whitespace-delimited token from a line
// and returns it if it looks like a file path.
// Handles: "cmd/game/main.go — Main entry point", "internal/game.go (state)"
func extractLeadingPath(line string) string {
	// Split on common separators: space, tab, em-dash, en-dash.
	separators := []string{" — ", " – ", " - ", "\t", " ("}
	candidate := line
	for _, sep := range separators {
		if idx := strings.Index(candidate, sep); idx > 0 {
			candidate = candidate[:idx]
		}
	}
	candidate = strings.TrimSpace(candidate)
	candidate = strings.Trim(candidate, "`*\"'")
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || strings.ContainsAny(candidate, " \t{}()[]<>") {
		return ""
	}
	if !strings.Contains(candidate, ".") || strings.Contains(candidate, "..") {
		return ""
	}
	candidate = strings.TrimPrefix(candidate, "./")
	return sanitizeFilePath(candidate, "")
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
// ```file=src/main.go, ```go:cmd/game/main.go, etc.
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

	// Handle lang:path format (e.g., "go:cmd/game/main.go", "python:src/app.py").
	if colonIdx := strings.Index(trimmed, ":"); colonIdx > 0 && colonIdx < len(trimmed)-1 {
		pathPart := strings.TrimSpace(trimmed[colonIdx+1:])
		pathPart = strings.TrimPrefix(pathPart, "./")
		if strings.Contains(pathPart, ".") && strings.Contains(pathPart, "/") &&
			!strings.ContainsAny(pathPart, " \t{}()[]<>") && !strings.Contains(pathPart, "..") {
			return pathPart
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
			// LLMs sometimes produce "package internal/foo" — use only the
			// last segment as the actual Go package name.
			if strings.Contains(pkg, "/") {
				parts := strings.Split(pkg, "/")
				pkg = parts[len(parts)-1]
			}
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

// isBinaryContent checks if content appears to be binary (non-text) data
// by looking for null bytes or a high ratio of non-printable characters
// in the first 512 bytes.
func isBinaryContent(data []byte) bool {
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	if checkLen == 0 {
		return false
	}
	nonPrintable := 0
	for _, b := range data[:checkLen] {
		if b == 0 {
			return true
		}
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			nonPrintable++
		}
	}
	return float64(nonPrintable)/float64(checkLen) > 0.1
}
