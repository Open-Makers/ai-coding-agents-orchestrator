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
)

// TesterPayload carries the list of source files written by the coder.
type TesterPayload struct {
	Files []string
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
// Used in the pipeline's Phase 2 where we only want test files on disk
// before the build-and-fix step.
func (a *TesterAgent) GenerateTests(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}
	return a.generateTests(ctx, files)
}

func (a *TesterAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {

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

// generateTests uses the LLM to create unit test files for the given source files.
func (a *TesterAgent) generateTests(ctx context.Context, files []string) error {
	var sourceContext strings.Builder

	// Include go.mod so LLM knows the module path and available dependencies.
	if gomod, err := os.ReadFile(filepath.Join(a.root, "go.mod")); err == nil {
		sourceContext.WriteString("**go.mod**\n```\n")
		sourceContext.Write(gomod)
		sourceContext.WriteString("\n```\n\n")
	}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(a.root, path))
		if err != nil {
			continue
		}
		fmt.Fprintf(&sourceContext, "**%s**\n```\n%s\n```\n\n", path, string(content))
	}

	if sourceContext.Len() == 0 {
		return nil
	}

	systemPrompt := prompts.MustLoad("tester-generate")

	userContent := fmt.Sprintf("Source files to test:\n\n%s", sourceContext.String())

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

	testFiles := a.writeTestBlocks(output)
	if len(testFiles) == 0 {
		return nil
	}

	return nil
}

// writeTestBlocks extracts file blocks from LLM output and writes them to disk.
// Returns the list of written file paths.
func (a *TesterAgent) writeTestBlocks(output string) []string {
	blocks := ExtractFileBlocks(output)
	var written []string
	for _, f := range blocks {
		target := filepath.Join(a.root, f.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			a.emitToken(fmt.Sprintf("warning: mkdir for test %s: %v\n", f.Path, err), false)
			continue
		}
		if err := os.WriteFile(target, []byte(f.Content), 0o644); err != nil {
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
		return []string{"go build ./...", "go test ./..."}
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
