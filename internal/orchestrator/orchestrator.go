package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	appctx "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	appprompts "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

// PipelineState represents the current phase of the pipeline.
type PipelineState string

const (
	PipelineIdle         PipelineState = "idle"
	PipelinePM           PipelineState = "pm"
	PipelineNegotiating  PipelineState = "negotiating"
	PipelineArchitecting PipelineState = "architect"
	PipelinePlanning     PipelineState = "planner"
	PipelineCoding       PipelineState = "coder"
	PipelineFixing       PipelineState = "coder_fixer"
	PipelineTesting      PipelineState = "tester"
	PipelineReviewing    PipelineState = "reviewer"
	PipelineUXReviewing  PipelineState = "ux_reviewer"
	PipelineSecurity     PipelineState = "security"
	PipelineDone         PipelineState = "done"
	PipelineGate         PipelineState = "human_gate"
	PipelineRateLimited  PipelineState = "rate_limited"
)

// Pipeline is the event-driven orchestrator for the legacy greenfield workflow.
// It drives agents through PM → Planner → staged (Code → quality gate).
// New code should prefer TaskRunner which unifies all work modes.
type Pipeline struct {
	b      *bus.Bus
	agents map[bus.AgentRole]agent.Agent
	cfg    config.Config
	ws     artifacts.Workspace
	root   string
	state  PipelineState
	log    *slog.Logger

	// quality is the shared quality gate component.
	quality *QualityGate

	// niceToHave collects non-blocking suggestions from all review phases.
	niceToHave map[string][]string

	// codingStarted is the timestamp of the first coder handoff (for elapsed timer).
	codingStarted time.Time

	// agentDurations accumulates wall-clock time spent in each agent role.
	agentDurations map[bus.AgentRole]time.Duration

	// humanCh carries replies from the user during PM conversation.
	humanCh chan string

	// gateCh receives a signal when a human gate is programmatically approved.
	gateCh chan struct{}

	// regenerateCh receives a signal when the user requests artifact regeneration.
	regenerateCh chan struct{}
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
	niceToHave := make(map[string][]string)
	agentDurations := make(map[bus.AgentRole]time.Duration)
	return &Pipeline{
		b:              b,
		agents:         agents,
		cfg:            cfg,
		ws:             ws,
		root:           root,
		state:          PipelineIdle,
		log:            logging.ForComponent("pipeline"),
		quality:        NewQualityGate(b, agents, cfg, ws, root, niceToHave, agentDurations),
		niceToHave:     niceToHave,
		agentDurations: agentDurations,
		humanCh:        make(chan string, 1),
		gateCh:         make(chan struct{}, 1),
		regenerateCh:   make(chan struct{}, 1),
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

// SendHumanReply delivers a user message during the PM conversation phase.
func (p *Pipeline) SendHumanReply(msg string) {
	select {
	case p.humanCh <- msg:
	default:
	}
}

// Regenerate signals the pipeline to delete existing artifacts and re-run the current agent.
func (p *Pipeline) Regenerate() {
	select {
	case <-p.regenerateCh:
	default:
	}
	p.regenerateCh <- struct{}{}
}

// CurrentState returns the current pipeline state.
func (p *Pipeline) CurrentState() PipelineState { return p.state }

// CodingStarted returns the timestamp when the coder was first invoked.
// Returns zero time if coding hasn't started yet.
func (p *Pipeline) CodingStarted() time.Time { return p.codingStarted }

// AgentDurations returns cumulative wall-clock time each agent has spent running.
func (p *Pipeline) AgentDurations() map[bus.AgentRole]time.Duration {
	result := make(map[bus.AgentRole]time.Duration, len(p.agentDurations))
	for k, v := range p.agentDurations {
		result[k] = v
	}
	return result
}

// ResumeBuildFix re-runs the coder's build-and-fix loop using the current project
// files on disk. Use this after a build-fix failure to give the coder another
// round of attempts without restarting the whole pipeline.
func (p *Pipeline) ResumeBuildFix(ctx context.Context) error {
	coder, ok := p.agents[bus.RoleCoder].(*agent.CoderAgent)
	if !ok {
		return fmt.Errorf("no coder agent available to resume")
	}
	p.setState(PipelineFixing)
	p.event("resuming build-and-fix loop…")
	files := p.collectProjectFiles(nil)
	if _, err := coder.BuildAndFix(ctx, files); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	p.setState(PipelineDone)
	return nil
}

// Run executes the full pipeline: PM → PLAN → per stage (CODE → quality gate).
// If requirementsPath is empty, the PM gathers requirements through a chat conversation.
func (p *Pipeline) Run(ctx context.Context, requirementsPath string) error {
	err := p.run(ctx, requirementsPath)
	if err != nil && errors.Is(err, runner.ErrRateLimited) && p.state != PipelineRateLimited {
		p.setState(PipelineRateLimited)
		p.event(fmt.Sprintf("⛔ rate limit / quota hit: %v — pipeline stopped", err))
	}
	return err
}

func (p *Pipeline) run(ctx context.Context, requirementsPath string) error {
	// Configure project-level prompt overrides before agents run.
	promptsDir := filepath.Join(p.root, artifacts.DirName, appprompts.PromptsDirName)
	appprompts.SetOverrideDir(promptsDir)

	var reqs []byte

	if requirementsPath != "" {
		var err error
		reqs, err = safefile.ReadFile(filepath.Dir(requirementsPath), filepath.Base(requirementsPath))
		if err != nil {
			return fmt.Errorf("read requirements: %w", err)
		}
		if err := p.ws.WriteFile(artifacts.RequirementsFile, reqs); err != nil {
			return err
		}
	}

	projCtx, err := appctx.Collect(p.root, p.cfg)
	if err != nil {
		p.event(fmt.Sprintf("context collect warning: %v", err))
	}
	ctxFragment := projCtx.SystemPromptFragment(appctx.ProfileFull)
	p.quality.SetProjectContext(projCtx)

	// ── PM Requirements Gathering (chat mode) ──
	if requirementsPath == "" {
		gatheredReqs, err := p.gatherRequirements(ctx, ctxFragment)
		if err != nil {
			return fmt.Errorf("gather requirements: %w", err)
		}
		reqs = []byte(gatheredReqs)
		if err := p.ws.WriteFile(artifacts.RequirementsFile, reqs); err != nil {
			return err
		}
	}

	// ── PM (Product Vision & MoSCoW) ──
	var moscowData, visionData []byte

	for {
		p.setState(PipelinePM)

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
			break
		}

		// Gate: user must approve PM output before planning begins.
		if len(visionData) > 0 || len(moscowData) > 0 {
			approved, err := p.waitPMApproval(ctx)
			if err != nil {
				return err
			}
			if approved {
				break
			}
			// Regeneration requested — delete PM artifacts and re-run.
			p.event("regenerating PM artifacts…")
			p.deletePMArtifacts()
			continue
		}
		break
	}

	// ── ARCHITECTURE ──
	for {
		p.setState(PipelineArchitecting)

		if p.ws.FileExists(artifacts.ArchitectureFile) {
			p.event("architecture document found from previous run — presenting for approval")
			p.emitExistingArchitecture()
		} else if _, ok := p.agents[bus.RoleArchitect]; ok {
			_, err = p.runAgent(ctx, bus.RoleArchitect, agent.ArchitectPayload{
				Requirements:   string(reqs),
				MoscowPlan:     string(moscowData),
				ProductVision:  string(visionData),
				ProjectContext: ctxFragment,
			})
			if err != nil {
				return fmt.Errorf("architect: %w", err)
			}
		} else {
			p.event("no architect agent configured — skipping architecture step")
			break
		}

		approved, err := p.waitArtifact(ctx, artifacts.ArchitectureFile)
		if err != nil {
			return err
		}
		if approved {
			break
		}
		p.event("regenerating architecture…")
		_ = os.Remove(p.ws.Path(artifacts.ArchitectureFile))
		_ = os.Remove(p.ws.Path(artifacts.ArchitectureApprovedFile))
	}

	archData, _ := p.ws.ReadFile(artifacts.ArchitectureFile)

	// ── PLAN ──
	for {
		p.setState(PipelinePlanning)

		if p.planningArtifactsExist() {
			p.event("planning artifacts found from previous run — presenting for approval")
			p.logger().Info("reusing existing planning artifacts")
			p.emitExistingArtifacts()
		} else {
			_, err = p.runAgent(ctx, bus.RolePlanner, agent.PlannerPayload{
				Requirements:   string(reqs),
				Architecture:   string(archData),
				MoscowPlan:     string(moscowData),
				ProductVision:  string(visionData),
				ProjectContext: ctxFragment,
			})
			if err != nil {
				return fmt.Errorf("plan: %w", err)
			}
		}

		approved, err := p.waitPlanningApproval(ctx)
		if err != nil {
			return err
		}
		if approved {
			break
		}
		// Regeneration requested — delete planning artifacts and re-run.
		p.event("regenerating planning artifacts…")
		p.deletePlanningArtifacts()
	}

	// ── CODE → TEST → REVIEW (staged) ──
	archData, _ = p.ws.ReadFile(artifacts.ArchitectureFile)
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
			if agent.IsBuildFixStuck(err) {
				cumulativeFiles = mergeFileList(cumulativeFiles, stageFiles)
				cumulativeFiles = p.collectProjectFiles(cumulativeFiles)
				p.event(fmt.Sprintf("stage %d/%d skipped after fix loop got stuck — continuing with current project state", stage.Index, totalStages))
				continue
			}
			return fmt.Errorf("stage %d (%s) code: %w", stage.Index, stage.Name, err)
		}

		cumulativeFiles = mergeFileList(cumulativeFiles, stageFiles)
		cumulativeFiles = p.collectProjectFiles(cumulativeFiles)

		if err := p.quality.RunChecks(ctx, ctxFragment, &cumulativeFiles); err != nil {
			if agent.IsBuildFixStuck(err) {
				cumulativeFiles = p.collectProjectFiles(cumulativeFiles)
				p.event(fmt.Sprintf("stage %d/%d quality gate got stuck in fix loop — continuing to next stage", stage.Index, totalStages))
				continue
			}
			return fmt.Errorf("stage %d (%s) quality gate: %w", stage.Index, stage.Name, err)
		}

		cumulativeFiles = p.collectProjectFiles(cumulativeFiles)
		p.event(fmt.Sprintf("stage %d/%d complete — %d files total", stage.Index, totalStages, len(cumulativeFiles)))
	}

	return nil
}

// collectProjectFiles merges existing cumulative files with any new source files
// found in the project directory. Only includes files with recognized source
// extensions to prevent binaries from polluting the context.
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
		// Only include files with recognized source code extensions.
		ext := strings.ToLower(filepath.Ext(rel))
		if ext == "" {
			return nil
		}
		if !seen[rel] {
			result = append(result, rel)
			seen[rel] = true
		}
		return nil
	})

	return agent.FilterSourceFiles(result)
}

// generateCode runs the TDD loop bootstrap: tests first, then coder,
// then initial build-and-test fix-up. Returns the list of files produced.
func (p *Pipeline) generateCode(ctx context.Context, plan, ctxFragment, stageName string, stageIndex, totalStages int, priorFiles []string) ([]string, error) {
	tester, hasTester := p.agents[bus.RoleTester].(*agent.TesterAgent)
	if hasTester {
		p.setState(PipelineTesting)
		p.event("writing tests first…")
		p.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RoleTester, bus.MsgRequest, "tdd-generate"))
		if err := tester.GenerateTests(ctx, agent.TesterPayload{
			Files:          priorFiles,
			Plan:           plan,
			ProjectContext: ctxFragment,
			StageName:      stageName,
		}); err != nil {
			p.event(fmt.Sprintf("warning: test generation: %v", err))
		}
		p.b.Publish(bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, "tests generated"))
	}

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

	// Initial build-and-test fix loop so source is validated against tests before quality gate.
	coder, ok := p.agents[bus.RoleCoder].(*agent.CoderAgent)
	if ok {
		p.setState(PipelineFixing)
		p.event("running build and tests…")
		fixed, buildErr := coder.BuildAndFix(ctx, coderResult.Files)
		if buildErr != nil {
			return coderResult.Files, fmt.Errorf("build: %w", buildErr)
		}
		coderResult.Files = fixed
	}

	return coderResult.Files, nil
}

func (p *Pipeline) runAgent(ctx context.Context, role bus.AgentRole, payload any) (bus.Message, error) {
	a, ok := p.agents[role]
	if !ok {
		return bus.Message{}, fmt.Errorf("no agent for role %q", role)
	}

	// Record the first coder handoff for the elapsed timer.
	if role == bus.RoleCoder && p.codingStarted.IsZero() {
		p.codingStarted = time.Now()
	}

	p.logger().Info("starting agent", slog.String("role", string(role)))
	msg := bus.NewMessage(bus.RoleSystem, role, bus.MsgRequest, payload)
	p.b.Publish(msg)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, role, bus.MsgEvent,
		fmt.Sprintf("starting %s", role)))

	started := time.Now()
	resp, err := a.Run(ctx, msg)
	elapsed := time.Since(started)
	p.agentDurations[role] += elapsed

	if err != nil {
		if errors.Is(err, runner.ErrRateLimited) {
			p.setState(PipelineRateLimited)
			p.b.Publish(bus.NewMessage(role, "", bus.MsgEvent,
				fmt.Sprintf("⛔ rate limit / quota hit: %v — pipeline stopped", err)))
		}
		p.logger().Error("agent failed",
			slog.String("role", string(role)),
			slog.String("error", err.Error()),
			slog.Duration("elapsed", elapsed),
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

// planningArtifactsExist returns true if both planner-owned artifacts
// (plan, prompts) already exist from a previous run. The architecture
// artifact is owned by the architect step and gated separately.
func (p *Pipeline) planningArtifactsExist() bool {
	return p.ws.FileExists(artifacts.ImplementationPlanFile) &&
		p.ws.FileExists(artifacts.PromptsFile)
}

// deletePMArtifacts removes PM artifacts so they can be regenerated.
func (p *Pipeline) deletePMArtifacts() {
	for _, f := range []string{
		artifacts.VisionFile,
		artifacts.VisionApprovedFile,
		artifacts.MoscowFile,
	} {
		_ = os.Remove(p.ws.Path(f))
	}
}

// deletePlanningArtifacts removes planner-owned artifacts so they can be regenerated.
// Architecture is owned by the architect step and deleted from there.
func (p *Pipeline) deletePlanningArtifacts() {
	for _, f := range []string{
		artifacts.ImplementationPlanFile,
		artifacts.PlanApprovedFile,
		artifacts.PromptsFile,
		artifacts.PromptsApprovedFile,
	} {
		_ = os.Remove(p.ws.Path(f))
	}
}

// emitExistingArtifacts publishes the content of existing planner artifacts
// (plan + prompts) to the bus so the TUI can display them.
func (p *Pipeline) emitExistingArtifacts() {
	for _, artifact := range []string{
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

// emitExistingArchitecture publishes the architecture document to the bus on
// the architect channel so the TUI can display it during the approval gate.
func (p *Pipeline) emitExistingArchitecture() {
	content, err := p.ws.ReadFile(artifacts.ArchitectureFile)
	if err != nil {
		return
	}
	p.b.Publish(bus.NewMessage(bus.RoleArchitect, "", bus.MsgEvent,
		bus.TokenPayload{Text: fmt.Sprintf("=== %s ===\n%s\n\n", artifacts.ArchitectureFile, string(content)), Done: false}))
	p.b.Publish(bus.NewMessage(bus.RoleArchitect, "", bus.MsgEvent,
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
// Returns false if the user requested regeneration.
func (p *Pipeline) waitPMApproval(ctx context.Context) (bool, error) {
	for _, filename := range []string{
		artifacts.VisionFile,
		artifacts.MoscowFile,
	} {
		approved, err := p.waitArtifact(ctx, filename)
		if err != nil {
			return false, err
		}
		if !approved {
			return false, nil
		}
	}
	return true, nil
}

// waitPlanningApproval gates on each planner-owned artifact in sequence.
// The architecture artifact is gated by the architect step.
// Returns false if the user requested regeneration.
func (p *Pipeline) waitPlanningApproval(ctx context.Context) (bool, error) {
	for _, filename := range []string{
		artifacts.ImplementationPlanFile,
		artifacts.PromptsFile,
	} {
		approved, err := p.waitArtifact(ctx, filename)
		if err != nil {
			return false, err
		}
		if !approved {
			return false, nil
		}
	}
	return true, nil
}

func (p *Pipeline) waitArtifact(ctx context.Context, filename string) (bool, error) {
	p.logger().Info("waiting for artifact approval", slog.String("artifact", filename))
	p.setState(PipelineGate)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgHumanGate, filename))
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-p.regenerateCh:
		p.logger().Info("artifact regeneration requested", slog.String("artifact", filename))
		return false, nil
	case <-p.gateCh:
		p.logger().Info("artifact approved", slog.String("artifact", filename))
		return true, nil
	}
}

// ReviseArtifact asks the responsible agent to revise an artifact based on user feedback.
// architecture.md is owned by the architect agent; other planning artifacts go to the planner.
func (p *Pipeline) ReviseArtifact(ctx context.Context, artifact, feedback string) error {
	if artifact == artifacts.ArchitectureFile {
		architect, ok := p.agents[bus.RoleArchitect].(*agent.ArchitectAgent)
		if !ok {
			return fmt.Errorf("architect agent not available for revision")
		}
		return architect.Revise(ctx, artifact, feedback)
	}
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

// gatherRequirements conducts PM↔human conversation to produce requirements.
// Returns the requirements text after both summary and requirements are approved.
func (p *Pipeline) gatherRequirements(ctx context.Context, ctxFragment string) (string, error) {
	p.setState(PipelineNegotiating)

	pm, ok := p.agents[bus.RolePM].(*agent.PMAgent)
	if !ok {
		return "", fmt.Errorf("no PM agent configured — cannot gather requirements via chat")
	}

	p.event("PM starting requirements conversation…")
	p.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RolePM, bus.MsgRequest, "gather"))

	started := time.Now()
	summary, requirements, err := pm.GatherRequirements(ctx, ctxFragment, p.humanCh)
	elapsed := time.Since(started)
	p.agentDurations[bus.RolePM] += elapsed

	if err != nil {
		return "", err
	}

	p.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgResponse, "requirements gathered"))

	// Save and present requirements for approval.
	if err := p.ws.WriteFile("gathered_summary.md", []byte(summary+"\n")); err != nil {
		return "", err
	}
	if err := p.ws.WriteFile(artifacts.RequirementsFile, []byte(requirements+"\n")); err != nil {
		return "", err
	}

	// Emit requirements for display.
	p.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgEvent,
		bus.TokenPayload{Text: "=== Requirements ===\n" + requirements + "\n\n", Done: false}))
	p.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgEvent,
		bus.TokenPayload{Text: "", Done: true}))

	// Gate: user must approve requirements before proceeding.
	p.setState(PipelineGate)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgHumanGate, artifacts.RequirementsFile))

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.regenerateCh:
		// User wants to re-do the conversation — recursive retry.
		p.event("re-gathering requirements…")
		_ = os.Remove(p.ws.Path(artifacts.RequirementsFile))
		_ = os.Remove(p.ws.Path("gathered_summary.md"))
		return p.gatherRequirements(ctx, ctxFragment)
	case <-p.gateCh:
		p.logger().Info("requirements approved")
		return requirements, nil
	}
}

func (p *Pipeline) event(msg string) {
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, msg))
}

// saveNiceToHave writes all collected nice-to-have suggestions to a markdown file.
func (p *Pipeline) saveNiceToHave() {
	saveNiceToHaveFile(p.ws, p.niceToHave, p.logger())
	total := totalNiceToHave(p.niceToHave)
	if total > 0 {
		p.event(fmt.Sprintf("saved %d nice-to-have suggestions to %s", total, artifacts.NiceToHaveFile))
	}
}

// emitSummary builds and emits a final pipeline summary.
func (p *Pipeline) emitSummary() {
	emitSummary(p.b, p.ws, p.agents, p.niceToHave, p.agentDurations, p.codingStarted)
}

// extractDeferredItems parses the plan for "Could Have" and "Won't Have" sections
// and adds them to nice-to-have so they appear in the final report.
func (p *Pipeline) extractDeferredItems(plan string) {
	extractDeferredItems(plan, p.niceToHave, func(msg string) { p.event(msg) })
}
