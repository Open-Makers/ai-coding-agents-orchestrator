package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	appctx "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	appprompts "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

const defaultMaxFixAttempts = 3

// PipelineState represents the current phase of the pipeline.
type PipelineState string

const (
	PipelineIdle        PipelineState = "idle"
	PipelinePM          PipelineState = "pm"
	PipelinePlanning    PipelineState = "planning"
	PipelineCoding      PipelineState = "coding"
	PipelineTesting     PipelineState = "testing"
	PipelineReviewing   PipelineState = "reviewing"
	PipelineUXReviewing PipelineState = "ux_reviewing"
	PipelineSecurity    PipelineState = "security"
	PipelineQA          PipelineState = "qa"
	PipelineDone        PipelineState = "done"
	PipelineGate        PipelineState = "human_gate"
)

// Pipeline is the event-driven orchestrator driving agents through the workflow.
type Pipeline struct {
	b      *bus.Bus
	agents map[bus.AgentRole]agent.Agent
	cfg    config.Config
	ws     artifacts.Workspace
	root   string
	state  PipelineState
	log    *slog.Logger

	// niceToHave collects non-blocking suggestions from all review phases.
	niceToHave map[string][]string

	// gateCh receives a signal when a human gate is programmatically approved.
	gateCh chan struct{}
}

// logger returns a non-nil logger, falling back to the default if p.log is nil.
func (p *Pipeline) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}
	return slog.Default()
}

func NewPipeline(
	b *bus.Bus,
	agents map[bus.AgentRole]agent.Agent,
	cfg config.Config,
	ws artifacts.Workspace,
	root string,
) *Pipeline {
	return &Pipeline{
		b:          b,
		agents:     agents,
		cfg:        cfg,
		ws:         ws,
		root:       root,
		state:      PipelineIdle,
		log:        logging.ForComponent("pipeline"),
		niceToHave: make(map[string][]string),
		gateCh:     make(chan struct{}, 1),
	}
}

// Approve unblocks the current human gate programmatically (used by TUI).
func (p *Pipeline) Approve() {
	select {
	case <-p.gateCh:
	default:
	}
	p.gateCh <- struct{}{}
}

// CurrentState returns the current pipeline state.
func (p *Pipeline) CurrentState() PipelineState { return p.state }

// maxFix returns the configured max fix attempts, falling back to the default.
// For local runners (ollama, opencode with local model) there is no limit.
func (p *Pipeline) maxFix() int {
	if p.isLocal() {
		return 0 // 0 means unlimited
	}
	if p.cfg.Project.MaxFixAttempts > 0 {
		return p.cfg.Project.MaxFixAttempts
	}
	return defaultMaxFixAttempts
}

// isLocal returns true when the coder agent uses a local runner.
func (p *Pipeline) isLocal() bool {
	if ac, ok := p.cfg.Agents["coder"]; ok {
		return runner.IsLocalRunner(ac)
	}
	return false
}

// Run executes the full pipeline: PM → PLAN → per stage (CODE → quality gate: TEST → REVIEW → UX → SECURITY → QA) → PR.
func (p *Pipeline) Run(ctx context.Context, requirementsPath string) error {
	// Configure project-level prompt overrides before agents run.
	promptsDir := filepath.Join(p.root, artifacts.DirName, appprompts.PromptsDirName)
	appprompts.SetOverrideDir(promptsDir)

	reqs, err := os.ReadFile(requirementsPath)
	if err != nil {
		return fmt.Errorf("read requirements: %w", err)
	}

	if err := p.ws.WriteFile(artifacts.RequirementsFile, reqs); err != nil {
		return err
	}

	projCtx, err := appctx.Collect(p.root, p.cfg)
	if err != nil {
		p.event(fmt.Sprintf("context collect warning: %v", err))
	}
	ctxFragment := projCtx.SystemPromptFragment()

	// ── PM (Product Vision & MoSCoW) ──
	p.setState(PipelinePM)

	var moscowData, visionData []byte
	if p.pmArtifactsExist() {
		p.event("PM artifacts found from previous run — presenting for approval")
		p.emitExistingPMArtifacts()
		moscowData, _ = p.ws.ReadFile(artifacts.MoscowFile)
		visionData, _ = p.ws.ReadFile(artifacts.VisionFile)
	} else if _, ok := p.agents[bus.RolePM]; ok {
		_, err = p.runAgent(ctx, bus.RolePM, agent.PMPayload{
			Requirements:   string(reqs),
			ProjectContext: ctxFragment,
		})
		if err != nil {
			return fmt.Errorf("pm: %w", err)
		}
		moscowData, _ = p.ws.ReadFile(artifacts.MoscowFile)
		visionData, _ = p.ws.ReadFile(artifacts.VisionFile)
	} else {
		p.event("no PM agent configured — planner will handle prioritization")
	}

	// Gate: user must approve PM output before planning begins.
	if len(visionData) > 0 || len(moscowData) > 0 {
		if err := p.waitPMApproval(ctx); err != nil {
			return err
		}
	}

	// ── PLAN ──
	p.setState(PipelinePlanning)

	if p.planningArtifactsExist() {
		p.event("planning artifacts found from previous run — presenting for approval")
		p.logger().Info("reusing existing planning artifacts")
		p.emitExistingArtifacts()
	} else {
		_, err = p.runAgent(ctx, bus.RolePlanner, agent.PlannerPayload{
			Requirements:   string(reqs),
			MoscowPlan:     string(moscowData),
			ProductVision:  string(visionData),
			ProjectContext: ctxFragment,
		})
		if err != nil {
			return fmt.Errorf("plan: %w", err)
		}
	}

	if err := p.waitPlanningApproval(ctx); err != nil {
		return err
	}

	// ── CODE → TEST → REVIEW (staged) ──
	archData, _ := p.ws.ReadFile(artifacts.ArchitectureFile)
	planData, _ := p.ws.ReadFile(artifacts.ImplementationPlanFile)
	promptsData, _ := p.ws.ReadFile(artifacts.PromptsFile)

	// Architecture is shared across all stages as context.
	architecture := string(archData)

	// Extract Could Have / Won't Have items from the plan as future work.
	p.extractDeferredItems(string(planData))

	stages := agent.ParseStages(string(promptsData))
	// If prompts yielded a single fallback stage, try extracting stages from the plan.
	if len(stages) == 1 && stages[0].Name == "Full Implementation" {
		planStages := agent.ParseStages(string(planData))
		if len(planStages) > 1 {
			p.event(fmt.Sprintf("extracted %d stages from plan (prompts had no stage delimiters)", len(planStages)))
			stages = planStages
		}
	}
	if err := p.stagedPipeline(ctx, architecture, ctxFragment, stages); err != nil {
		return err
	}

	p.saveNiceToHave()

	p.setState(PipelineDone)
	p.emitSummary()
	p.event("run complete")
	return nil
}

// stagedPipeline iterates over planner stages. Each stage goes through the full
// cycle: coder → build → test → reviewer → ux_reviewer → security → qa.
// If any phase finds must-fix issues, the coder fixes them and the entire
// quality gate restarts from build/test. Files accumulate across stages.
func (p *Pipeline) stagedPipeline(ctx context.Context, architecture, ctxFragment string, stages []agent.Stage) error {
	var cumulativeFiles []string
	totalStages := len(stages)

	for _, stage := range stages {
		stageName := fmt.Sprintf("Stage %d/%d: %s", stage.Index, totalStages, stage.Name)
		p.event(fmt.Sprintf("── %s ──", stageName))

		stagePlan := "## Architecture\n\n" + architecture + "\n\n## Stage Instructions\n\n" + stage.Prompt

		stageFiles, err := p.generateCode(ctx, stagePlan, ctxFragment, stageName, stage.Index, totalStages, cumulativeFiles)
		if err != nil {
			return fmt.Errorf("stage %d (%s) code: %w", stage.Index, stage.Name, err)
		}

		cumulativeFiles = mergeFileList(cumulativeFiles, stageFiles)
		cumulativeFiles = p.collectProjectFiles(cumulativeFiles)

		if err := p.qualityGate(ctx, ctxFragment, &cumulativeFiles); err != nil {
			return fmt.Errorf("stage %d (%s) quality gate: %w", stage.Index, stage.Name, err)
		}

		cumulativeFiles = p.collectProjectFiles(cumulativeFiles)
		p.event(fmt.Sprintf("stage %d/%d complete — %d files total", stage.Index, totalStages, len(cumulativeFiles)))
	}

	return nil
}

// collectProjectFiles merges existing cumulative files with any new source files
// found in the project directory.
func (p *Pipeline) collectProjectFiles(existing []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		seen[f] = true
	}
	result := make([]string, len(existing))
	copy(result, existing)

	_ = filepath.Walk(p.root, func(path string, info os.FileInfo, err error) error {
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
		rel, _ := filepath.Rel(p.root, path)
		if rel == "" || strings.HasPrefix(rel, ".") {
			return nil
		}
		if !seen[rel] {
			result = append(result, rel)
			seen[rel] = true
		}
		return nil
	})

	return result
}

// generateCode runs the Coder for initial code generation, generates tests,
// and performs initial build-and-fix. Returns the list of files produced.
func (p *Pipeline) generateCode(ctx context.Context, plan, ctxFragment, stageName string, stageIndex, totalStages int, priorFiles []string) ([]string, error) {
	p.setState(PipelineCoding)
	coderResp, err := p.runAgent(ctx, bus.RoleCoder, agent.CoderPayload{
		Plan:           plan,
		ProjectContext: ctxFragment,
		StageName:      stageName,
		StageIndex:     stageIndex,
		TotalStages:    totalStages,
		PriorFiles:     priorFiles,
	})
	if err != nil {
		return nil, fmt.Errorf("code: %w", err)
	}

	coderResult := extractCoderResult(coderResp)

	// Generate test files.
	tester, hasTester := p.agents[bus.RoleTester].(*agent.TesterAgent)
	if hasTester && len(coderResult.Files) > 0 {
		p.setState(PipelineTesting)
		p.event("generating tests…")
		p.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RoleTester, bus.MsgRequest, "generate"))
		if err := tester.GenerateTests(ctx, coderResult.Files); err != nil {
			p.event(fmt.Sprintf("warning: test generation: %v", err))
		}
		p.b.Publish(bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, "tests generated"))
	}

	// Initial build-and-fix so source compiles before quality gate.
	coder, ok := p.agents[bus.RoleCoder].(*agent.CoderAgent)
	if ok {
		p.setState(PipelineCoding)
		p.event("building project…")
		allFiles := appendTestFiles(coderResult.Files, p.root)
		fixed, buildErr := coder.BuildAndFix(ctx, allFiles)
		if buildErr != nil {
			return coderResult.Files, fmt.Errorf("build: %w", buildErr)
		}
		coderResult.Files = fixed
	}

	return coderResult.Files, nil
}

// qualityGate runs the full quality pipeline in a single loop:
// test → review → ux_review → security → qa.
// If any phase finds must-fix issues, the coder fixes them and the loop restarts.
func (p *Pipeline) qualityGate(ctx context.Context, ctxFragment string, files *[]string) error {
	maxAttempts := p.maxFix()

	for attempt := 0; maxAttempts == 0 || attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			p.event(fmt.Sprintf("quality gate pass %d", attempt+1))
		}

		// 1. Test
		failure, err := p.runTests(ctx, *files)
		if err != nil {
			return err
		}
		if failure != "" {
			if err := p.fixAndRebuild(ctx, ctxFragment, failure, files, "test", attempt, maxAttempts); err != nil {
				return err
			}
			continue
		}

		// 2. Code review
		mustFix, err := p.runReview(ctx)
		if err != nil {
			return err
		}
		if mustFix != "" {
			if err := p.fixAndRebuild(ctx, ctxFragment, mustFix, files, "review", attempt, maxAttempts); err != nil {
				return err
			}
			continue
		}

		// 3. UX review
		mustFix, err = p.runUXReview(ctx)
		if err != nil {
			return err
		}
		if mustFix != "" {
			if err := p.fixAndRebuild(ctx, ctxFragment, "UX/UI ISSUES:\n"+mustFix, files, "ux_review", attempt, maxAttempts); err != nil {
				return err
			}
			continue
		}

		// 4. Security review
		mustFix, err = p.runSecurityReview(ctx)
		if err != nil {
			return err
		}
		if mustFix != "" {
			if err := p.fixAndRebuild(ctx, ctxFragment, "SECURITY ISSUES:\n"+mustFix, files, "security", attempt, maxAttempts); err != nil {
				return err
			}
			continue
		}

		// 5. QA review
		mustFix, err = p.runQAReview(ctx)
		if err != nil {
			return err
		}
		if mustFix != "" {
			if err := p.fixAndRebuild(ctx, ctxFragment, "QA CORNER CASE ISSUES:\n"+mustFix, files, "qa", attempt, maxAttempts); err != nil {
				return err
			}
			continue
		}

		// All phases passed.
		p.event("quality gate passed — all checks clean")
		return nil
	}

	return fmt.Errorf("quality gate still failing after %d attempts", maxAttempts)
}

// runTests executes the tester agent and returns failure description (empty on success).
func (p *Pipeline) runTests(ctx context.Context, files []string) (string, error) {
	p.setState(PipelineTesting)
	testResp, err := p.runAgent(ctx, bus.RoleTester, agent.TesterPayload{Files: files})
	if err != nil {
		return "", fmt.Errorf("test: %w", err)
	}

	testResult, ok := testResp.Payload.(agent.TestReport)
	if !ok {
		return "", fmt.Errorf("tester returned unexpected payload type %T", testResp.Payload)
	}

	if testResult.Success {
		p.event("all tests passed")
		return "", nil
	}

	return buildTestFailure(testResult), nil
}

// runReview runs the code reviewer agent and returns must-fix issues (empty on approval).
func (p *Pipeline) runReview(ctx context.Context) (string, error) {
	p.setState(PipelineReviewing)
	reviewResp, err := p.runAgent(ctx, bus.RoleReviewer, agent.ReviewerPayload{})
	if err != nil {
		return "", fmt.Errorf("review: %w", err)
	}

	result, ok := reviewResp.Payload.(agent.ReviewResult)
	if !ok {
		return "", fmt.Errorf("reviewer returned unexpected payload type %T", reviewResp.Payload)
	}

	p.collectNiceToHave("Code Review", result.NiceToHave)

	if len(result.MustFix) == 0 {
		p.event("review passed — no must-fix issues")
		return "", nil
	}

	return strings.Join(result.MustFix, "\n"), nil
}

// runUXReview runs the UX reviewer agent and returns must-fix issues (empty on approval).
func (p *Pipeline) runUXReview(ctx context.Context) (string, error) {
	if _, ok := p.agents[bus.RoleUXReviewer]; !ok {
		p.event("no UX reviewer agent configured — skipping")
		return "", nil
	}

	p.setState(PipelineUXReviewing)
	uxResp, err := p.runAgent(ctx, bus.RoleUXReviewer, agent.UXReviewerPayload{})
	if err != nil {
		return "", fmt.Errorf("ux review: %w", err)
	}

	result, ok := uxResp.Payload.(agent.UXReviewResult)
	if !ok {
		return "", fmt.Errorf("ux reviewer returned unexpected payload type %T", uxResp.Payload)
	}

	p.collectNiceToHave("UX/UI", result.NiceToHave)

	if len(result.MustFix) == 0 {
		p.event("UX review passed — no must-fix issues")
		return "", nil
	}

	return strings.Join(result.MustFix, "\n"), nil
}

// runSecurityReview runs the security agent and returns must-fix issues (empty on approval).
func (p *Pipeline) runSecurityReview(ctx context.Context) (string, error) {
	if _, ok := p.agents[bus.RoleSecurity]; !ok {
		p.event("no security agent configured — skipping")
		return "", nil
	}

	p.setState(PipelineSecurity)
	secResp, err := p.runAgent(ctx, bus.RoleSecurity, agent.SecurityPayload{})
	if err != nil {
		return "", fmt.Errorf("security: %w", err)
	}

	result, ok := secResp.Payload.(agent.SecurityResult)
	if !ok {
		return "", fmt.Errorf("security agent returned unexpected payload type %T", secResp.Payload)
	}

	p.collectNiceToHave("Security", result.NiceToHave)

	if len(result.MustFix) == 0 {
		p.event("security review passed — no must-fix issues")
		return "", nil
	}

	return strings.Join(result.MustFix, "\n"), nil
}

// runQAReview runs the QA agent and returns must-fix issues (empty on approval).
func (p *Pipeline) runQAReview(ctx context.Context) (string, error) {
	if _, ok := p.agents[bus.RoleQA]; !ok {
		p.event("no QA agent configured — skipping")
		return "", nil
	}

	p.setState(PipelineQA)
	qaResp, err := p.runAgent(ctx, bus.RoleQA, agent.QAPayload{})
	if err != nil {
		return "", fmt.Errorf("qa: %w", err)
	}

	result, ok := qaResp.Payload.(agent.QAResult)
	if !ok {
		return "", fmt.Errorf("qa agent returned unexpected payload type %T", qaResp.Payload)
	}

	p.collectNiceToHave("QA", result.NiceToHave)

	if len(result.MustFix) == 0 {
		p.event("QA review passed — no must-fix issues")
		return "", nil
	}

	return strings.Join(result.MustFix, "\n"), nil
}

// fixAndRebuild sends failure to coder, rebuilds, and returns.
// The caller (qualityGate) will restart the full quality pipeline.
func (p *Pipeline) fixAndRebuild(ctx context.Context, ctxFragment, failure string, files *[]string, phase string, attempt, maxAttempts int) error {
	if maxAttempts > 0 && attempt >= maxAttempts-1 {
		return fmt.Errorf("%s issues not resolved after %d attempts", phase, maxAttempts)
	}

	if maxAttempts > 0 {
		p.event(fmt.Sprintf("%s issues found, sending to coder (attempt %d/%d)", phase, attempt+1, maxAttempts))
	} else {
		p.event(fmt.Sprintf("%s issues found, sending to coder (attempt %d)", phase, attempt+1))
	}

	allFiles := appendTestFiles(*files, p.root)
	p.setState(PipelineCoding)
	fixResp, err := p.runAgent(ctx, bus.RoleCoder, agent.CoderFixPayload{
		Failure:        failure,
		ProjectContext: ctxFragment,
		Files:          allFiles,
	})
	if err != nil {
		return fmt.Errorf("code fix (%s): %w", phase, err)
	}

	fixResult := extractCoderResult(fixResp)
	*files = mergeFileList(*files, fixResult.Files)
	*files = p.collectProjectFiles(*files)

	// Rebuild after fix.
	coder, ok := p.agents[bus.RoleCoder].(*agent.CoderAgent)
	if ok {
		p.setState(PipelineCoding)
		p.event("rebuilding after fix…")
		fixed, buildErr := coder.BuildAndFix(ctx, *files)
		if buildErr != nil {
			return fmt.Errorf("rebuild after %s fix: %w", phase, buildErr)
		}
		*files = fixed
	}

	return nil
}

func (p *Pipeline) runAgent(ctx context.Context, role bus.AgentRole, payload any) (bus.Message, error) {
	a, ok := p.agents[role]
	if !ok {
		return bus.Message{}, fmt.Errorf("no agent for role %q", role)
	}
	p.logger().Info("starting agent", slog.String("role", string(role)))
	msg := bus.NewMessage(bus.RoleSystem, role, bus.MsgRequest, payload)
	p.b.Publish(msg)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, role, bus.MsgEvent,
		fmt.Sprintf("starting %s", role)))

	resp, err := a.Run(ctx, msg)
	if err != nil {
		p.logger().Error("agent failed",
			slog.String("role", string(role)),
			slog.String("error", err.Error()),
		)
		p.b.Publish(bus.NewMessage(role, "", bus.MsgEvent,
			fmt.Sprintf("error: %v", err)))
		return bus.Message{}, err
	}
	p.logger().Info("agent completed", slog.String("role", string(role)))
	p.b.Publish(bus.NewMessage(role, "", bus.MsgResponse,
		fmt.Sprintf("%s complete", role)))
	return resp, nil
}

// pmArtifactsExist returns true if PM artifacts already exist from a previous run.
func (p *Pipeline) pmArtifactsExist() bool {
	return p.ws.FileExists(artifacts.VisionFile) &&
		p.ws.FileExists(artifacts.MoscowFile)
}

// planningArtifactsExist returns true if all three planning artifacts
// (architecture, plan, prompts) already exist from a previous run.
func (p *Pipeline) planningArtifactsExist() bool {
	return p.ws.FileExists(artifacts.ArchitectureFile) &&
		p.ws.FileExists(artifacts.ImplementationPlanFile) &&
		p.ws.FileExists(artifacts.PromptsFile)
}

// emitExistingArtifacts publishes the content of existing planning artifacts
// to the bus so the TUI can display them.
func (p *Pipeline) emitExistingArtifacts() {
	for _, artifact := range []string{
		artifacts.ArchitectureFile,
		artifacts.ImplementationPlanFile,
		artifacts.PromptsFile,
	} {
		content, err := p.ws.ReadFile(artifact)
		if err != nil {
			continue
		}
		p.b.Publish(bus.NewMessage(bus.RolePlanner, "", bus.MsgEvent,
			bus.TokenPayload{Text: fmt.Sprintf("=== %s ===\n%s\n\n", artifact, string(content)), Done: false}))
	}
	p.b.Publish(bus.NewMessage(bus.RolePlanner, "", bus.MsgEvent,
		bus.TokenPayload{Text: "", Done: true}))
}

// emitExistingPMArtifacts publishes PM artifacts to the bus so the TUI can display them.
func (p *Pipeline) emitExistingPMArtifacts() {
	for _, artifact := range []string{
		artifacts.VisionFile,
		artifacts.MoscowFile,
	} {
		content, err := p.ws.ReadFile(artifact)
		if err != nil {
			continue
		}
		p.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgEvent,
			bus.TokenPayload{Text: fmt.Sprintf("=== %s ===\n%s\n\n", artifact, string(content)), Done: false}))
	}
	p.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgEvent,
		bus.TokenPayload{Text: "", Done: true}))
}

// waitPMApproval gates on PM artifacts (vision + MoSCoW) before planning.
func (p *Pipeline) waitPMApproval(ctx context.Context) error {
	for _, filename := range []string{
		artifacts.VisionFile,
		artifacts.MoscowFile,
	} {
		if err := p.waitArtifact(ctx, filename); err != nil {
			return err
		}
	}
	return nil
}

// waitPlanningApproval gates on each planning artifact in sequence.
func (p *Pipeline) waitPlanningApproval(ctx context.Context) error {
	for _, filename := range []string{
		artifacts.ArchitectureFile,
		artifacts.ImplementationPlanFile,
		artifacts.PromptsFile,
	} {
		if err := p.waitArtifact(ctx, filename); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) waitArtifact(ctx context.Context, filename string) error {
	p.logger().Info("waiting for artifact approval", slog.String("artifact", filename))
	p.setState(PipelineGate)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgHumanGate, filename))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.gateCh:
		p.logger().Info("artifact approved", slog.String("artifact", filename))
		return nil
	}
}

// ReviseArtifact asks the planner agent to revise an artifact based on user feedback.
func (p *Pipeline) ReviseArtifact(ctx context.Context, artifact, feedback string) error {
	planner, ok := p.agents[bus.RolePlanner].(*agent.PlannerAgent)
	if !ok {
		return fmt.Errorf("planner agent not available for revision")
	}
	return planner.Revise(ctx, artifact, feedback)
}

func (p *Pipeline) setState(s PipelineState) {
	p.state = s
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, string(s)))
}

func (p *Pipeline) event(msg string) {
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, msg))
}

// collectNiceToHave appends suggestions from a review phase.
func (p *Pipeline) collectNiceToHave(phase string, items []string) {
	if len(items) == 0 {
		return
	}
	p.niceToHave[phase] = append(p.niceToHave[phase], items...)
	p.logger().Info("collected nice-to-have items",
		slog.String("phase", phase),
		slog.Int("count", len(items)),
	)
}

// saveNiceToHave writes all collected nice-to-have suggestions to a markdown file.
func (p *Pipeline) saveNiceToHave() {
	if len(p.niceToHave) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("# Nice to Have / Recommendations\n\n")
	sb.WriteString("Items deferred from MoSCoW plan and suggestions from review phases.\n\n")

	for _, phase := range []string{"Could Have (from plan)", "Won't Have (from plan)", "Code Review", "UX/UI", "Security", "QA"} {
		items := p.niceToHave[phase]
		if len(items) == 0 {
			continue
		}
		sb.WriteString("## " + phase + "\n\n")
		for _, item := range items {
			sb.WriteString("- " + item + "\n")
		}
		sb.WriteString("\n")
	}

	content := sb.String()
	if err := p.ws.WriteFile(artifacts.NiceToHaveFile, []byte(content)); err != nil {
		p.logger().Warn("failed to write nice-to-have file", slog.String("error", err.Error()))
	}
	p.event(fmt.Sprintf("saved %d nice-to-have suggestions to %s", p.totalNiceToHave(), artifacts.NiceToHaveFile))
}

func (p *Pipeline) totalNiceToHave() int {
	total := 0
	for _, items := range p.niceToHave {
		total += len(items)
	}
	return total
}

// emitSummary builds and emits a final summary visible in the PR agent panel.
func (p *Pipeline) emitSummary() {
	var sb strings.Builder
	sb.WriteString("\n════════════════════════════════════════\n")
	sb.WriteString("  PIPELINE COMPLETE — SUMMARY\n")
	sb.WriteString("════════════════════════════════════════\n\n")

	// Review statuses.
	phases := []struct {
		name string
		role bus.AgentRole
		file string
	}{
		{"Code Review", bus.RoleReviewer, artifacts.ReviewFile},
		{"UX/UI Review", bus.RoleUXReviewer, artifacts.UXReviewFile},
		{"Security Audit", bus.RoleSecurity, artifacts.SecurityReviewFile},
		{"QA Review", bus.RoleQA, artifacts.QAReviewFile},
	}

	for _, ph := range phases {
		if _, ok := p.agents[ph.role]; !ok {
			sb.WriteString(fmt.Sprintf("  ○ %s — skipped\n", ph.name))
			continue
		}
		if p.ws.FileExists(ph.file) {
			sb.WriteString(fmt.Sprintf("  ✓ %s — passed\n", ph.name))
		} else {
			sb.WriteString(fmt.Sprintf("  ? %s — no output\n", ph.name))
		}
	}

	// Nice-to-have summary.
	total := p.totalNiceToHave()
	if total > 0 {
		sb.WriteString(fmt.Sprintf("\n  📋 %d nice-to-have suggestions saved to %s\n", total, artifacts.NiceToHaveFile))
		for phase, items := range p.niceToHave {
			sb.WriteString(fmt.Sprintf("     • %s: %d items\n", phase, len(items)))
		}
	}

	// Artifacts.
	sb.WriteString("\n  Artifacts:\n")
	for _, file := range []string{
		artifacts.ReviewFile, artifacts.UXReviewFile,
		artifacts.SecurityReviewFile, artifacts.QAReviewFile,
		artifacts.NiceToHaveFile,
	} {
		if p.ws.FileExists(file) {
			sb.WriteString(fmt.Sprintf("    • %s\n", file))
		}
	}

	sb.WriteString("\n════════════════════════════════════════\n")

	summary := sb.String()

	// Save to file.
	_ = p.ws.WriteFile(artifacts.SummaryFile, []byte(summary))

	// Emit summary to system event.
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent,
		bus.TokenPayload{Text: summary, Done: false}))
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent,
		bus.TokenPayload{Text: "", Done: true}))
}

// extractDeferredItems parses the plan for "Could Have" and "Won't Have" sections
// and adds them to nice-to-have so they appear in the final report.
func (p *Pipeline) extractDeferredItems(plan string) {
	lines := strings.Split(plan, "\n")
	section := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		switch {
		case strings.Contains(upper, "COULD HAVE"):
			section = "could"
		case strings.Contains(upper, "WON'T HAVE") || strings.Contains(upper, "WONT HAVE") || strings.Contains(upper, "WON'T HAVE"):
			section = "wont"
		case strings.Contains(upper, "MUST HAVE") || strings.Contains(upper, "SHOULD HAVE"):
			section = ""
		case strings.HasPrefix(upper, "## ") || strings.HasPrefix(upper, "==="):
			// Other section headers reset.
			if section != "" && !strings.Contains(upper, "COULD") && !strings.Contains(upper, "WON") {
				section = ""
			}
		default:
			if section == "" {
				continue
			}
			item := strings.TrimSpace(strings.TrimLeft(trimmed, "-*0123456789.)"))
			if item == "" {
				continue
			}
			label := "Could Have (from plan)"
			if section == "wont" {
				label = "Won't Have (from plan)"
			}
			p.niceToHave[label] = append(p.niceToHave[label], item)
		}
	}

	total := len(p.niceToHave["Could Have (from plan)"]) + len(p.niceToHave["Won't Have (from plan)"])
	if total > 0 {
		p.event(fmt.Sprintf("extracted %d deferred items from plan (Could Have + Won't Have)", total))
	}
}

func buildTestFailure(test agent.TestReport) string {
	var parts []string
	for _, c := range test.Commands {
		if c.ExitCode != 0 {
			parts = append(parts, fmt.Sprintf("$ %s\n%s\n%s", c.Command, c.Stdout, c.Stderr))
		}
	}
	return strings.Join(parts, "\n")
}

// extractCoderResult safely extracts CoderResult from a message, returning
// an empty result if the payload type doesn't match.
func extractCoderResult(msg bus.Message) agent.CoderResult {
	if cr, ok := msg.Payload.(agent.CoderResult); ok {
		return cr
	}
	return agent.CoderResult{}
}

// mergeFileList returns a combined file list without duplicates.
func mergeFileList(existing, added []string) []string {
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

// appendTestFiles scans directories of source files for *_test.go files
// and appends them to the list, so the coder can see and fix tests too.
func appendTestFiles(files []string, root string) []string {
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f] = true
	}

	result := make([]string, len(files))
	copy(result, files)

	dirs := make(map[string]bool)
	for _, f := range files {
		dirs[filepath.Dir(f)] = true
	}

	for dir := range dirs {
		absDir := filepath.Join(root, dir)
		entries, err := os.ReadDir(absDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			rel := filepath.Join(dir, e.Name())
			if !seen[rel] {
				result = append(result, rel)
				seen[rel] = true
			}
		}
	}
	return result
}
