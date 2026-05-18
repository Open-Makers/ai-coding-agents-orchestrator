package agent

import (
	"bufio"
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
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

// TesterPayload carries the implementation plan and/or source files for test generation.
type TesterPayload struct {
	Files          []string
	Plan           string
	ProjectContext string
	StageName      string
}

// TesterUpdatePayload asks the tester to update existing test files to match
// the current production contract. Used by the quality gate when a reviewer
// flags the tests-vs-implementation disagreement and the coder is forbidden
// from writing _test.go files itself.
type TesterUpdatePayload struct {
	Failure        string
	Files          []string // production source files (read-only context)
	TestFiles      []string // existing test files the tester may modify
	ProjectContext string
}

// TestReport is the structured result written to test_report.json.
type TestReport struct {
	Success  bool              `json:"success"`
	Commands []executil.Result `json:"commands"`
}

// TesterAgent generates unit tests via LLM, writes them to disk, and runs them.
type TesterAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	root   string
	cfg    config.Config
	exec   *executil.Runner
	skills []string
	model  string
}

func NewTesterAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, root string, cfg config.Config, skills []string, model string) *TesterAgent {
	return &TesterAgent{
		BaseAgent: NewBase(bus.RoleTester, b),
		runner:    r,
		ws:        ws,
		root:      root,
		cfg:       cfg,
		exec:      executil.NewRunner(root),
		skills:    skills,
		model:     model,
	}
}

func (a *TesterAgent) Role() bus.AgentRole { return bus.RoleTester }

// GenerateTests creates test files via LLM without running them.
// Used in TDD mode where tests are written before or alongside implementation.
func (a *TesterAgent) GenerateTests(ctx context.Context, payload TesterPayload) error {
	if len(payload.Files) == 0 && strings.TrimSpace(payload.Plan) == "" {
		return nil
	}
	return a.generateTests(ctx, payload)
}

// UpdateTests rewrites existing test files so they match the latest
// production contract. Returns the list of test files that were updated.
//
// The tester is the sole owner of *_test.go files (the coder is blocked from
// writing them by writeOneFile). Without this method, every reviewer-driven
// contract change would either fail the build silently or burn fix attempts.
func (a *TesterAgent) UpdateTests(ctx context.Context, payload TesterUpdatePayload) ([]string, error) {
	if len(payload.TestFiles) == 0 || strings.TrimSpace(payload.Failure) == "" {
		return nil, nil
	}

	systemPrompt := prompts.MustLoad("tester-update")
	if strings.TrimSpace(payload.ProjectContext) != "" {
		systemPrompt = fmt.Sprintf("%s\n\nProject context:\n%s", systemPrompt, payload.ProjectContext)
	}

	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Failure / reviewer feedback:\n%s\n\n", payload.Failure)

	if len(payload.Files) > 0 {
		b.WriteString("Production source files (READ-ONLY — source of truth):\n\n")
		for _, p := range payload.Files {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			content, err := safefile.ReadFile(a.root, p)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(&b, "**%s**\n```\n%s\n```\n\n", p, string(content))
		}
	}

	b.WriteString("Existing test files (UPDATE THESE):\n\n")
	for _, p := range payload.TestFiles {
		content, err := safefile.ReadFile(a.root, p)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(&b, "**%s**\n```\n%s\n```\n\n", p, string(content))
	}

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return nil, fmt.Errorf("tester update: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return nil, fmt.Errorf("tester update: stream: %w", err)
	}

	return a.writeTestBlocks(output), nil
}

func (a *TesterAgent) Run(_ context.Context, _ bus.Message) (bus.Message, error) {

	report := TestReport{Success: true}

	// Install dependencies before running tests.
	depResult := a.installDeps()
	if depResult != nil && depResult.ExitCode != 0 {
		report.Success = false
		report.Commands = append(report.Commands, *depResult)
		// Dependency install failed — no point running tests.
		if err := a.ws.WriteJSON(artifacts.TestReportFile, report); err != nil {
			return bus.Message{}, err
		}
		a.emitToken("", true)
		return bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, report), nil
	}

	data, err := a.ws.ReadFile(artifacts.TestCmdsFile)
	if err != nil {
		data = nil
	}

	cmds := parseCmds(string(data))
	if len(cmds) == 0 {
		cmds = a.fallbackTestCmds()
		a.emitToken(fmt.Sprintf("no test commands from coder, using defaults: %v\n", cmds), false)
	}

	for _, c := range cmds {
		if isInteractiveCommand(c) {
			a.emitToken(fmt.Sprintf("skipping interactive command: %s\n", c), false)
			continue
		}
		a.emitToken(fmt.Sprintf("$ %s\n", c), false)
		res := a.exec.Run(c)
		report.Commands = append(report.Commands, res)
		a.emitToken(res.Stdout+"\n"+res.Stderr+"\n", false)
		if res.ExitCode != 0 {
			report.Success = false
		}

		if err := a.ws.WriteJSON(artifacts.TestReportFile, report); err != nil {
			return bus.Message{}, err
		}

		// Stop on build failure but continue for test failures to collect all results.
		if !report.Success && strings.Contains(c, "build") {
			break
		}
	}
	a.emitToken("", true)
	return bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, report), nil
}

// generateTests uses the LLM to create unit test files from the plan and/or source files.
func (a *TesterAgent) generateTests(ctx context.Context, payload TesterPayload) error {
	var promptParts []string

	// Include go.mod so LLM knows the module path and available dependencies.
	if gomod, err := safefile.ReadFile(a.root, "go.mod"); err == nil {
		promptParts = append(promptParts, fmt.Sprintf("**go.mod**\n```\n%s\n```", string(gomod)))
	}

	var sourceContext strings.Builder
	for _, path := range payload.Files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		content, err := safefile.ReadFile(a.root, path)
		if err != nil {
			continue
		}
		_, err = fmt.Fprintf(&sourceContext, "**%s**\n```\n%s\n```\n\n", path, string(content))
		if err != nil {
			return err
		}
	}

	if strings.TrimSpace(payload.Plan) != "" {
		header := "Implementation plan"
		if strings.TrimSpace(payload.StageName) != "" {
			header = payload.StageName
		}
		promptParts = append(promptParts, fmt.Sprintf("**%s**\n%s", header, payload.Plan))
	}

	if sourceContext.Len() > 0 {
		promptParts = append(promptParts, "Source files to test:\n\n"+sourceContext.String())
	}

	if len(promptParts) == 0 {
		return nil
	}

	systemPrompt := prompts.MustLoad("tester-generate")
	if strings.TrimSpace(payload.ProjectContext) != "" {
		systemPrompt = fmt.Sprintf("%s\n\nProject context:\n%s", systemPrompt, payload.ProjectContext)
	}
	userContent := strings.Join(promptParts, "\n\n")

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return fmt.Errorf("tester: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return fmt.Errorf("tester: stream: %w", err)
	}

	sections := parseSections(output, "TEST_CMDS")
	if cmds := strings.TrimSpace(sections["TEST_CMDS"]); cmds != "" {
		if err := a.ws.WriteFile(artifacts.TestCmdsFile, []byte(cmds+"\n")); err != nil {
			return err
		}
	}

	testFiles := a.writeTestBlocks(output)
	if len(testFiles) == 0 {
		a.emitToken("tester produced 0 test files — saving raw output for inspection\n", false)
		if err := a.ws.WriteFile(artifacts.TesterRawOutputFile, []byte(output)); err != nil {
			a.emitToken(fmt.Sprintf("warning: failed to save tester raw output: %v\n", err), false)
		} else {
			a.emitToken(fmt.Sprintf("raw tester output saved to %s\n", artifacts.TesterRawOutputFile), false)
		}
		return nil
	}
	a.emitToken(fmt.Sprintf("tester wrote %d test file(s)\n", len(testFiles)), false)
	return nil
}

// writeTestBlocks extracts file blocks from LLM output and writes them to disk.
// Returns the list of written file paths. Non-test paths are rejected so a
// confused tester cannot overwrite production source files.
func (a *TesterAgent) writeTestBlocks(output string) []string {
	blocks := ExtractFileBlocks(output)
	var written []string
	for _, f := range blocks {
		if !strings.HasSuffix(f.Path, "_test.go") {
			a.emitToken(fmt.Sprintf("warning: refusing non-test path from tester: %s\n", f.Path), false)
			continue
		}
		target := filepath.Join(a.root, f.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			a.emitToken(fmt.Sprintf("warning: mkdir for test %s: %v\n", f.Path, err), false)
			continue
		}
		if err := os.WriteFile(target, []byte(f.Content), 0o600); err != nil {
			a.emitToken(fmt.Sprintf("warning: write test %s: %v\n", f.Path, err), false)
			continue
		}
		a.emitToken(fmt.Sprintf("wrote test: %s\n", f.Path), false)
		written = append(written, f.Path)
	}
	return written
}

// installDeps detects the project language and runs the appropriate
// dependency install command before tests. Returns the result if a command was run.
func (a *TesterAgent) installDeps() *executil.Result {
	lang := a.detectLanguage()
	var depCmd string
	switch lang {
	case "go":
		depCmd = "go mod tidy"
	case "node", "javascript", "typescript":
		if fileExists(filepath.Join(a.root, "yarn.lock")) {
			depCmd = "yarn install"
		} else if fileExists(filepath.Join(a.root, "pnpm-lock.yaml")) {
			depCmd = "pnpm install"
		} else {
			depCmd = "npm install"
		}
	case "python":
		if fileExists(filepath.Join(a.root, "requirements.txt")) {
			depCmd = "pip install -r requirements.txt"
		} else if fileExists(filepath.Join(a.root, "pyproject.toml")) {
			depCmd = "pip install -e ."
		}
	case "rust":
		depCmd = "cargo fetch"
	}

	if depCmd == "" {
		return nil
	}

	a.emitToken(fmt.Sprintf("$ %s\n", depCmd), false)
	res := a.exec.Run(depCmd)
	if res.ExitCode != 0 {
		a.emitToken(fmt.Sprintf("warning: dep install failed: %s\n", res.Stderr), false)
	}
	return &res
}

// detectLanguage determines the project language from config or project files.
func (a *TesterAgent) detectLanguage() string {
	if a.cfg.Project.Language != "" {
		return a.cfg.Project.Language
	}
	markers := map[string]string{
		"go.mod":           "go",
		"package.json":     "node",
		"Cargo.toml":       "rust",
		"requirements.txt": "python",
		"pyproject.toml":   "python",
		"Gemfile":          "ruby",
		"pom.xml":          "java",
		"build.gradle":     "java",
	}
	for file, lang := range markers {
		if fileExists(filepath.Join(a.root, file)) {
			return lang
		}
	}
	return ""
}

// fallbackTestCmds returns default test commands based on config or language.
func (a *TesterAgent) fallbackTestCmds() []string {
	if a.cfg.Project.TestCmd != "" {
		return []string{"go build ./...", a.cfg.Project.TestCmd}
	}
	lang := a.detectLanguage()
	switch lang {
	case "go":
		return []string{"go build ./...", "go test -count=1 ./..."}
	case "node", "javascript", "typescript":
		return []string{"npm test"}
	case "python":
		return []string{"python -m pytest"}
	case "rust":
		return []string{"cargo build", "cargo test"}
	default:
		return []string{"echo 'no test commands available'"}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseCmds(input string) []string {
	var cmds []string
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		cleaned := stripListMarker(line)
		cleaned = strings.Trim(cleaned, "`")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}
		if !looksLikeCommand(cleaned) {
			continue
		}
		cmds = append(cmds, cleaned)
	}
	return cmds
}

func stripListMarker(s string) string {
	if len(s) > 2 && (s[0] == '-' || s[0] == '*') && s[1] == ' ' {
		return strings.TrimSpace(s[2:])
	}
	for i, c := range s {
		if c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == ')') && i+1 < len(s) && s[i+1] == ' ' {
			return strings.TrimSpace(s[i+2:])
		}
		break
	}
	return s
}

func looksLikeCommand(line string) bool {
	cmdPrefixes := []string{
		"go ", "make", "npm ", "yarn ", "pnpm ", "cargo ", "python ", "pip ",
		"pytest", "ruby ", "bundle ", "mvn ", "gradle ", "docker ", "kubectl ",
		"curl ", "wget ", "cat ", "echo ", "ls ", "cd ", "mkdir ", "rm ",
		"cp ", "mv ", "chmod ", "sh ", "bash ", "test ", "./", "set ",
	}
	lower := strings.ToLower(line)
	for _, p := range cmdPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return strings.HasPrefix(line, "$ ")
}

// isInteractiveCommand returns true for commands that may require user input
// or run indefinitely (e.g. go run, servers, REPLs).
func isInteractiveCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))

	interactivePrefixes := []string{
		"go run ",
		"python -c ",
		"node -e ",
		"npm start",
		"yarn start",
		"npm run dev",
		"yarn dev",
	}
	for _, prefix := range interactivePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	// Piped go run is also interactive (stdin closes but program may not exit).
	if strings.Contains(lower, "| go run") || strings.Contains(lower, "|go run") {
		return true
	}

	return false
}
