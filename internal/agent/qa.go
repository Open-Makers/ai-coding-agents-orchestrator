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
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// QATestPayload carries inputs for TDD test generation.
type QATestPayload struct {
	Files          []string
	Plan           string
	ProjectContext string
	StageName      string
}

// QAVerifyTestsPayload asks QA to check whether existing tests are correct
// after the coder has failed multiple attempts.
type QAVerifyTestsPayload struct {
	Failure        string
	Files          []string // production source files (read-only context)
	TestFiles      []string // existing test files QA may modify
	ProjectContext string
}

// QAVerifyResult is the outcome of a test verification.
type QAVerifyResult struct {
	TestsOK      bool     // true = tests are correct, coder must try harder
	UpdatedFiles []string // test files that were rewritten (empty when TestsOK)
	Explanation  string   // brief reason for the verdict
}

// QAReviewPayload carries inputs for quality review.
type QAReviewPayload struct {
	Files          []string
	Root           string
	ProjectContext string
	Seeds          []string
}

// QAReviewResult is the structured outcome of a quality review.
type QAReviewResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
	Unparsed   bool
	RawOutput  string
}

// QAAgent combines test writing (TDD), test verification, and quality review.
type QAAgent struct {
	BaseAgent
	runner           runner.LLMRunner
	ws               artifacts.Workspace
	root             string
	cfg              config.Config
	exec             *executil.Runner
	skills           []string
	model            string
	maxContextTokens int
}

func NewQAAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, root string, cfg config.Config, skills []string, model string) *QAAgent {
	return &QAAgent{
		BaseAgent: NewBase(bus.RoleQA, b),
		runner:    r,
		ws:        ws,
		root:      root,
		cfg:       cfg,
		exec:      executil.NewRunner(root),
		skills:    skills,
		model:     model,
	}
}

// SetMaxContextTokens configures the token budget for review mode.
func (a *QAAgent) SetMaxContextTokens(n int) { a.maxContextTokens = n }

func (a *QAAgent) Role() bus.AgentRole { return bus.RoleQA }

// GenerateTests creates test files via LLM in TDD mode (tests before implementation).
func (a *QAAgent) GenerateTests(ctx context.Context, payload QATestPayload) error {
	if len(payload.Files) == 0 && strings.TrimSpace(payload.Plan) == "" {
		return nil
	}
	return a.generateTests(ctx, payload)
}

// VerifyTests checks whether existing tests are correct after repeated coder failures.
// Returns a verdict: either tests are OK (coder's problem) or updated test files.
func (a *QAAgent) VerifyTests(ctx context.Context, payload QAVerifyTestsPayload) (QAVerifyResult, error) {
	if len(payload.TestFiles) == 0 || strings.TrimSpace(payload.Failure) == "" {
		return QAVerifyResult{TestsOK: true, Explanation: "no tests to verify"}, nil
	}

	systemPrompt := prompts.MustLoad("qa-verify-tests")
	if strings.TrimSpace(payload.ProjectContext) != "" {
		systemPrompt = fmt.Sprintf("%s\n\nProject context:\n%s", systemPrompt, payload.ProjectContext)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Failure / coder feedback:\n%s\n\n", payload.Failure)

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
			fmt.Fprintf(&b, "**%s**\n```\n%s\n```\n\n", p, string(content))
		}
	}

	b.WriteString("Existing test files (VERIFY / UPDATE THESE):\n\n")
	for _, p := range payload.TestFiles {
		content, err := safefile.ReadFile(a.root, p)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "**%s**\n```\n%s\n```\n\n", p, string(content))
	}

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return QAVerifyResult{}, fmt.Errorf("qa verify: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return QAVerifyResult{}, fmt.Errorf("qa verify: stream: %w", err)
	}

	if strings.Contains(output, "===VERDICT: TESTS_OK===") {
		return QAVerifyResult{
			TestsOK:     true,
			Explanation: extractAfterMarker(output, "===VERDICT: TESTS_OK==="),
		}, nil
	}

	updated := a.writeTestBlocks(output)
	return QAVerifyResult{
		TestsOK:      false,
		UpdatedFiles: updated,
		Explanation:  "tests updated",
	}, nil
}

// Run executes a quality review (code quality, logic, corner cases).
func (a *QAAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	switch p := msg.Payload.(type) {
	case QAReviewPayload:
		return a.runReview(ctx, p)
	case QATestPayload:
		return a.runTests(ctx)
	default:
		return bus.Message{}, fmt.Errorf("qa: unexpected payload type %T", msg.Payload)
	}
}

func (a *QAAgent) runReview(ctx context.Context, payload QAReviewPayload) (bus.Message, error) {
	plan, _ := a.ws.ReadFile(artifacts.ImplementationPlanFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	sourceContext := buildCompactSourceContext(payload.Root, payload.Files, a.maxContextTokens, payload.Seeds...)
	if sourceContext == "" {
		raw, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
		sourceContext = string(raw)
	}

	systemPrompt := prompts.MustLoad("qa-review")
	if strings.TrimSpace(payload.ProjectContext) != "" {
		systemPrompt = fmt.Sprintf("%s\n\nProject context:\n%s", systemPrompt, payload.ProjectContext)
	}

	userContent := fmt.Sprintf("Plan:\n%s\n\nCode:\n%s\n\nTest results:\n%s",
		string(plan), sourceContext, string(report))

	if a.maxContextTokens > 0 {
		userContent = tokenutil.Truncate(userContent, a.maxContextTokens)
	}

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("qa review: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("qa review: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.ReviewFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	result := parseQAReview(output)
	return bus.NewMessage(bus.RoleQA, "", bus.MsgResponse, result), nil
}

func (a *QAAgent) runTests(ctx context.Context) (bus.Message, error) {
	report := TestReport{Success: true}

	depResult := a.installDeps()
	if depResult != nil && depResult.ExitCode != 0 {
		report.Success = false
		report.Commands = append(report.Commands, *depResult)
		if err := a.ws.WriteJSON(artifacts.TestReportFile, report); err != nil {
			return bus.Message{}, err
		}
		a.emitToken("", true)
		return bus.NewMessage(bus.RoleQA, "", bus.MsgResponse, report), nil
	}

	data, err := a.ws.ReadFile(artifacts.TestCmdsFile)
	if err != nil {
		data = nil
	}

	cmds := parseCmds(string(data))
	if len(cmds) == 0 {
		cmds = a.fallbackTestCmds()
		a.emitToken(fmt.Sprintf("no test commands, using defaults: %v\n", cmds), false)
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

		if !report.Success && strings.Contains(c, "build") {
			break
		}
	}
	a.emitToken("", true)
	return bus.NewMessage(bus.RoleQA, "", bus.MsgResponse, report), nil
}

func (a *QAAgent) generateTests(ctx context.Context, payload QATestPayload) error {
	var promptParts []string

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
		fmt.Fprintf(&sourceContext, "**%s**\n```\n%s\n```\n\n", path, string(content))
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

	systemPrompt := prompts.MustLoad("qa-tests")
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
		return fmt.Errorf("qa tests: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return fmt.Errorf("qa tests: stream: %w", err)
	}

	sections := parseSections(output, "TEST_CMDS")
	if cmds := strings.TrimSpace(sections["TEST_CMDS"]); cmds != "" {
		if err := a.ws.WriteFile(artifacts.TestCmdsFile, []byte(cmds+"\n")); err != nil {
			return err
		}
	}

	testFiles := a.writeTestBlocks(output)
	if len(testFiles) == 0 {
		a.emitToken("QA produced 0 test files — saving raw output for inspection\n", false)
		if err := a.ws.WriteFile(artifacts.TesterRawOutputFile, []byte(output)); err != nil {
			a.emitToken(fmt.Sprintf("warning: failed to save QA raw output: %v\n", err), false)
		}
		return nil
	}
	a.emitToken(fmt.Sprintf("QA wrote %d test file(s)\n", len(testFiles)), false)
	return nil
}

func (a *QAAgent) writeTestBlocks(output string) []string {
	blocks := ExtractFileBlocks(output)
	var written []string
	for _, f := range blocks {
		if !strings.HasSuffix(f.Path, "_test.go") {
			a.emitToken(fmt.Sprintf("warning: refusing non-test path from QA: %s\n", f.Path), false)
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

func (a *QAAgent) installDeps() *executil.Result {
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

func (a *QAAgent) detectLanguage() string {
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

func (a *QAAgent) fallbackTestCmds() []string {
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

func parseQAReview(text string) QAReviewResult {
	s := parseReviewSections(text, "NICE TO HAVE", "RECOMMENDATION")
	return QAReviewResult{
		Approved:   s.Approved,
		MustFix:    s.MustFix,
		NiceToHave: s.NiceToHave,
		Unparsed:   !s.Parsed,
		RawOutput:  text,
	}
}

// extractAfterMarker returns text following a marker line, trimmed.
func extractAfterMarker(text, marker string) string {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	// Take first non-empty line after the marker.
	scanner := bufio.NewScanner(strings.NewReader(rest))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			return line
		}
	}
	return ""
}
