package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/beads"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	appctx "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	appprompts "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// defaultMaxCoderAttempts is the number of times the coder may fail before
// escalating to QA to verify whether the tests are correct.
const defaultMaxCoderAttempts = 3

// maxQAVerifyRounds caps QA test-verification rounds to prevent infinite loops
// when neither the coder nor QA can resolve the failure.
const maxQAVerifyRounds = 2

// maxArbitrateIterations caps the outer PM-arbitrate loop (review → fix → review).
const maxArbitrateIterations = 3

// TaskRunner is the unified orchestrator that treats everything as a task.
// All work flows through: negotiate → decompose → per-bead (QA tests → coder) →
// quality review → PM arbitrate → done.
type TaskRunner struct {
	b              *bus.Bus
	agents         map[bus.AgentRole]agent.Agent
	cfg            config.Config
	ws             artifacts.Workspace
	root           string
	log            *slog.Logger
	state          PipelineState
	niceToHave     map[string][]string
	agentDurations map[bus.AgentRole]time.Duration
	codingStarted  time.Time

	// projCtx is the project context collected at the start of a run; reused
	// for scoped-context shadow measurement during review. Best-effort.
	projCtx   appctx.ProjectContext
	taskQuery string // task description used as the semantic-search query

	// humanCh carries replies from the user during negotiation.
	humanCh chan string

	// gateCh receives approval for the task spec.
	gateCh chan struct{}

	// regenerateCh receives a signal to re-negotiate.
	regenerateCh chan struct{}
}

// NewTaskRunner creates a TaskRunner with all agent dependencies.
func NewTaskRunner(
	b *bus.Bus,
	agents map[bus.AgentRole]agent.Agent,
	cfg config.Config,
	ws artifacts.Workspace,
	root string,
) *TaskRunner {
	return &TaskRunner{
		b:              b,
		agents:         agents,
		cfg:            cfg,
		ws:             ws,
		root:           root,
		log:            logging.ForComponent("task-runner"),
		state:          PipelineIdle,
		niceToHave:     make(map[string][]string),
		agentDurations: make(map[bus.AgentRole]time.Duration),
		humanCh:        make(chan string, 1),
		gateCh:         make(chan struct{}, 1),
		regenerateCh:   make(chan struct{}, 1),
	}
}

// CurrentState returns the current pipeline state.
func (tr *TaskRunner) CurrentState() PipelineState { return tr.state }

// CodingStarted returns the timestamp when the coder was first invoked.
func (tr *TaskRunner) CodingStarted() time.Time { return tr.codingStarted }

// AgentDurations returns cumulative wall-clock time each agent has spent running.
func (tr *TaskRunner) AgentDurations() map[bus.AgentRole]time.Duration {
	result := make(map[bus.AgentRole]time.Duration, len(tr.agentDurations))
	for k, v := range tr.agentDurations {
		result[k] = v
	}
	return result
}

// SendHumanReply delivers a user message during the negotiation phase.
func (tr *TaskRunner) SendHumanReply(msg string) {
	select {
	case tr.humanCh <- msg:
	default:
	}
}

// Approve unblocks the task spec approval gate.
func (tr *TaskRunner) Approve() {
	select {
	case <-tr.gateCh:
	default:
	}
	tr.gateCh <- struct{}{}
}

// Regenerate signals the TaskRunner to re-negotiate.
func (tr *TaskRunner) Regenerate() {
	select {
	case <-tr.regenerateCh:
	default:
	}
	tr.regenerateCh <- struct{}{}
}

// Run executes the full task lifecycle:
// 1. Negotiate with PM (human in the loop)
// 2. PM plans execution strategy (autonomous)
// 3. Execute: code → quality gate (fully autonomous)
func (tr *TaskRunner) Run(ctx context.Context, taskInput string) error {
	err := tr.run(ctx, taskInput)
	if err != nil && errors.Is(err, runner.ErrRateLimited) && tr.state != PipelineRateLimited {
		tr.setState(PipelineRateLimited)
		tr.event(fmt.Sprintf("⛔ rate limit / quota hit: %v — pipeline stopped", err))
	}
	return err
}

func (tr *TaskRunner) run(ctx context.Context, taskInput string) (retErr error) {
	promptsDir := filepath.Join(tr.root, artifacts.DirName, appprompts.PromptsDirName)
	appprompts.SetOverrideDir(promptsDir)
	appprompts.SetUserLanguage("English")

	var (
		curStage = "init"
		curSpec  agent.TaskSpec
	)
	curSpec.Title = strings.TrimSpace(firstLine(taskInput))
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic at stage %s: %v", curStage, r)
		}
		if retErr != nil {
			tr.abortMemory(curSpec, curStage, retErr)
		}
	}()

	curStage = "collect-context"
	projCtx, err := appctx.Collect(tr.root, tr.cfg)
	if err != nil {
		tr.event(fmt.Sprintf("context collect warning: %v", err))
	}
	tr.projCtx = projCtx
	tr.taskQuery = strings.TrimSpace(taskInput)

	if recalled, mErr := appctx.CollectMemory(ctx, tr.root, tr.cfg, taskInput); mErr != nil {
		tr.event(fmt.Sprintf("memory recall warning: %v", mErr))
		projCtx.Memory = recalled
	} else {
		projCtx.Memory = recalled
		if len(recalled.Hits) > 0 || recalled.Pinned != "" {
			tr.event(fmt.Sprintf("memory: recalled %d fragment(s) from past work", len(recalled.Hits)))
		}
	}

	ctxFragment := projCtx.SystemPromptFragment(appctx.ProfileFull)

	tr.memAppend("task-start", fmt.Sprintf("**Task input:**\n\n%s", strings.TrimSpace(taskInput)))

	// ── Phase 1: Negotiate ── (human in the loop)
	curStage = "negotiate"
	var spec agent.TaskSpec
	for {
		spec, err = tr.negotiate(ctx, taskInput, ctxFragment)
		if err != nil {
			return fmt.Errorf("negotiate: %w", err)
		}

		if projCtx.IsBrownfield && strings.EqualFold(spec.Scope, "greenfield") {
			newScope := inferBrownfieldScope(taskInput, spec.Description)
			tr.event(fmt.Sprintf("brownfield project detected — downgrading scope from greenfield to %s", newScope))
			spec.Scope = newScope
		}

		specData, _ := json.MarshalIndent(spec, "", "  ")
		if err := tr.ws.WriteFile(artifacts.TaskSpecFile, specData); err != nil {
			return fmt.Errorf("write task spec: %w", err)
		}

		approved, err := tr.waitArtifact(ctx, artifacts.TaskSpecFile)
		if err != nil {
			return err
		}
		if approved {
			curSpec = spec
			tr.memAppend("spec-approved", fmt.Sprintf("**Title:** %s\n\n**Scope:** %s\n\n**Description:**\n\n%s",
				spec.Title, spec.Scope, strings.TrimSpace(spec.Description)))
			break
		}
		tr.event("re-negotiating task spec…")
		_ = removeWorkspaceFile(tr.ws, artifacts.TaskSpecFile)
	}

	beadID := tr.registerBead(ctx, spec)

	// ── Phase 2: Decompose ── (PM creates sub-tasks / beads)
	curStage = "decompose"
	subTasks, err := tr.runDecomposeGate(ctx, spec, "", ctxFragment, projCtx.IsBrownfield)
	if err != nil {
		return fmt.Errorf("decompose: %w", err)
	}
	tr.memAppend("plan-done", fmt.Sprintf("Decomposed into %d sub-task(s).", len(subTasks)))

	subBeadIDs := tr.createSubTaskBeads(ctx, subTasks)

	// ── Phase 3+4: Implement + Quality Review loop ──
	curStage = "execute"
	tr.bootstrapForTDD(spec)

	if err := tr.implementAndReview(ctx, spec, ctxFragment, subTasks, subBeadIDs); err != nil {
		return err
	}

	curStage = "finalise"
	saveNiceToHaveFile(tr.ws, tr.niceToHave, tr.log)
	tr.setState(PipelineDone)
	emitSummary(tr.b, tr.ws, tr.agents, tr.niceToHave, tr.agentDurations, tr.codingStarted)
	tr.finaliseMemory(ctx, spec)
	tr.closeBead(ctx, beadID, "Completed by orchestrator")
	tr.event("task complete")
	return nil
}

// implementAndReview runs the execute→review→arbitrate loop.
// Phase 3: Execute all beads (QA writes tests, Coder implements per bead).
// Phase 4: Quality review (QA + UX + Security), PM arbitrates.
// If PM finds real issues, new beads are created and the loop restarts.
func (tr *TaskRunner) implementAndReview(ctx context.Context, spec agent.TaskSpec, ctxFragment string, subTasks []agent.SubTask, subBeadIDs map[string]string) error {
	tasks := subTasks
	beadIDs := subBeadIDs

	for iter := 0; iter < maxArbitrateIterations; iter++ {
		// ── Phase 3: Execute beads ──
		if err := tr.executeBeads(ctx, spec, ctxFragment, tasks, beadIDs); err != nil {
			return err
		}

		// ── Phase 4: Quality review ──
		files := collectProjectFilesFromRoot(nil, tr.root)

		qaFeedback, uxFeedback, secFeedback := tr.qualityReview(ctx, ctxFragment, files)

		if qaFeedback == "" && uxFeedback == "" && secFeedback == "" {
			tr.event("quality review passed — all checks clean")
			return nil
		}

		// PM arbitrates all feedback in a single call.
		pm, ok := tr.agents[bus.RolePM].(*agent.PMAgent)
		if !ok {
			tr.event("no PM agent for arbitration — accepting review as pass")
			return nil
		}

		tr.setState(PipelinePM)
		tr.event("PM arbitrating review feedback…")

		started := time.Now()
		verdict, err := pm.ArbitrateAll(ctx, qaFeedback, uxFeedback, secFeedback)
		tr.agentDurations[bus.RolePM] += time.Since(started)

		if err != nil {
			return fmt.Errorf("pm arbitrate: %w", err)
		}

		// Record nice-to-have from PM.
		if len(verdict.NiceToHave) > 0 {
			tr.niceToHave["PM Deferred"] = append(tr.niceToHave["PM Deferred"], verdict.NiceToHave...)
		}

		if verdict.Pass || len(verdict.SubTasks) == 0 {
			tr.event("PM verdict: all clear — no fixes needed")
			return nil
		}

		tr.event(fmt.Sprintf("PM verdict: %d issue(s) to fix (review scope: %s)", len(verdict.SubTasks), verdict.ReviewScope))

		// Create new beads for the fix tasks.
		tasks = verdict.SubTasks
		beadIDs = tr.createSubTaskBeads(ctx, tasks)
	}

	tr.event(fmt.Sprintf("quality review loop exhausted after %d iterations — remaining issues moved to nice-to-have", maxArbitrateIterations))
	return nil
}

// negotiate conducts PM↔human conversation to produce a TaskSpec.
func (tr *TaskRunner) negotiate(ctx context.Context, input, ctxFragment string) (agent.TaskSpec, error) {
	tr.setState(PipelineNegotiating)

	pm, ok := tr.agents[bus.RolePM].(*agent.PMAgent)
	if !ok {
		// No PM agent: create a minimal spec directly from the input.
		tr.event("no PM agent configured — using input as task description")
		return agent.TaskSpec{
			Title:       "Task",
			Description: input,
			Scope:       "feature",
		}, nil
	}

	tr.event("PM analyzing task…")
	tr.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RolePM, bus.MsgRequest, "negotiate"))

	started := time.Now()
	spec, err := pm.NegotiateTask(ctx, input, ctxFragment, tr.humanCh)
	elapsed := time.Since(started)
	tr.agentDurations[bus.RolePM] += elapsed

	if err != nil {
		return agent.TaskSpec{}, err
	}

	tr.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgResponse, "negotiation complete"))

	// Emit the TaskSpec for visibility; approval gate is handled by the caller.
	specData, _ := json.MarshalIndent(spec, "", "  ")
	tr.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgTaskSpec, string(specData)))

	return spec, nil
}

// runDecomposeGate asks PM to split the spec into sub-tasks, renders them
// to task_plan.md for the human gate, and re-decomposes on rejection. Returns
// the approved sub-task list.
func (tr *TaskRunner) runDecomposeGate(ctx context.Context, spec agent.TaskSpec, architecture, ctxFragment string, brownfield bool) ([]agent.SubTask, error) {
	tr.setState(PipelinePlanning)
	pm, ok := tr.agents[bus.RolePM].(*agent.PMAgent)
	if !ok {
		// No PM agent: fall back to a single sub-task built from the spec.
		return []agent.SubTask{{
			Key:         "T1",
			Title:       spec.Title,
			Description: spec.Description,
			Priority:    2,
		}}, nil
	}

	for {
		tr.event("PM decomposing task into sub-tasks…")
		tr.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RolePM, bus.MsgRequest, "decompose"))

		started := time.Now()
		tasks, err := pm.DecomposeTask(ctx, spec, architecture, ctxFragment, brownfield)
		tr.agentDurations[bus.RolePM] += time.Since(started)
		if err != nil {
			return nil, err
		}
		tr.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgResponse, fmt.Sprintf("decomposed into %d sub-tasks", len(tasks))))

		if err := tr.ws.WriteFile(artifacts.TaskPlanFile, []byte(renderTaskPlan(tasks))); err != nil {
			return nil, fmt.Errorf("write task plan: %w", err)
		}

		approved, err := tr.waitArtifact(ctx, artifacts.TaskPlanFile)
		if err != nil {
			return nil, err
		}
		if approved {
			return tasks, nil
		}
		tr.event("re-decomposing task…")
		_ = removeWorkspaceFile(tr.ws, artifacts.TaskPlanFile)
	}
}

// renderTaskPlan turns the decomposed sub-tasks into a human-readable markdown
// document for the task_plan.md approval gate.
func renderTaskPlan(tasks []agent.SubTask) string {
	var sb strings.Builder
	sb.WriteString("# Task Plan\n\n")
	fmt.Fprintf(&sb, "PM decomposed the work into **%d** sub-task(s). Each one becomes a Beads issue and a single coder pass.\n\n", len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(&sb, "## %s — %s\n\n", t.Key, t.Title)
		fmt.Fprintf(&sb, "Priority: %d\n", t.Priority)
		if len(t.DependsOn) > 0 {
			sb.WriteString("Depends on: ")
			sb.WriteString(strings.Join(t.DependsOn, ", "))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSpace(t.Description))
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// createSubTaskBeads persists each approved sub-task as a Beads issue and
// records its dependencies. Returns a map from sub-task Key to bead ID for
// callers that want to drive execution by key (or look up dependency status).
// Failures are non-fatal: any sub-task that fails to register is logged and
// skipped; the executor falls back to in-process ordering if no beads exist.
func (tr *TaskRunner) createSubTaskBeads(ctx context.Context, tasks []agent.SubTask) map[string]string {
	if !beads.Available() {
		tr.event("bd not installed — skipping bead creation; execution will run sub-tasks in order")
		return nil
	}
	idByKey := make(map[string]string, len(tasks))
	for _, t := range tasks {
		id, err := beads.Create(ctx, tr.root, t.Title, t.Description, t.Priority)
		if err != nil {
			tr.event(fmt.Sprintf("warning: bd create %s: %v", t.Key, err))
			continue
		}
		idByKey[t.Key] = id
		for _, depKey := range t.DependsOn {
			parentID, ok := idByKey[depKey]
			if !ok {
				tr.event(fmt.Sprintf("warning: %s declares unknown dependency %q — skipped", t.Key, depKey))
				continue
			}
			if err := beads.AddDependency(ctx, tr.root, id, parentID); err != nil {
				tr.event(fmt.Sprintf("warning: bd dep add %s→%s: %v", id, parentID, err))
			}
		}
		tr.event(fmt.Sprintf("registered %s as %s — %s", t.Key, id, t.Title))
	}
	return idByKey
}

// executeBeads drives implementation by polling `bd ready`, claiming beads,
// and running a QA-tests → Coder loop per bead. Falls back to in-order
// execution when the `bd` binary is unavailable.
func (tr *TaskRunner) executeBeads(ctx context.Context, spec agent.TaskSpec, ctxFragment string, tasks []agent.SubTask, idByKey map[string]string) error {
	if !beads.Available() || len(idByKey) == 0 {
		return tr.executeSubTasksInOrder(ctx, spec, ctxFragment, tasks)
	}

	total := len(tasks)
	done := 0
	for {
		ready, err := beads.Ready(ctx, tr.root)
		if err != nil {
			tr.event(fmt.Sprintf("warning: bd ready: %v — falling back to in-order execution", err))
			return tr.executeSubTasksInOrder(ctx, spec, ctxFragment, tasks)
		}
		if len(ready) == 0 {
			return nil
		}

		bead := ready[0]
		if err := beads.Claim(ctx, tr.root, bead.ID); err != nil {
			tr.event(fmt.Sprintf("warning: bd claim %s: %v", bead.ID, err))
		}
		done++
		tr.event(fmt.Sprintf("── Sub-task %d/%d (%s): %s ──", done, total, bead.ID, bead.Title))

		if err := tr.executeBead(ctx, ctxFragment, bead.Title, bead.Description); err != nil {
			return fmt.Errorf("bead %s: %w", bead.ID, err)
		}
		if err := beads.Close(ctx, tr.root, bead.ID, "Completed by orchestrator"); err != nil {
			tr.event(fmt.Sprintf("warning: bd close %s: %v", bead.ID, err))
		}
	}
}

// executeSubTasksInOrder runs sub-tasks sequentially when `bd` is unavailable.
func (tr *TaskRunner) executeSubTasksInOrder(ctx context.Context, spec agent.TaskSpec, ctxFragment string, tasks []agent.SubTask) error {
	for i, t := range tasks {
		tr.event(fmt.Sprintf("── Sub-task %d/%d: %s ──", i+1, len(tasks), t.Title))
		if err := tr.executeBead(ctx, ctxFragment, t.Title, t.Description); err != nil {
			return fmt.Errorf("sub-task %s: %w", t.Key, err)
		}
	}
	return nil
}

// executeBead runs a single bead: QA writes tests (TDD), then Coder
// implements in a loop until tests pass. If coder fails N times, QA
// verifies whether the tests are correct.
func (tr *TaskRunner) executeBead(ctx context.Context, ctxFragment, title, description string) error {
	plan := title + "\n\n" + description

	// Step 1: QA writes tests (TDD).
	qa, hasQA := tr.agents[bus.RoleQA].(*agent.QAAgent)
	if hasQA {
		tr.setState(PipelineQATests)
		tr.event("QA writing tests (TDD)…")
		tr.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RoleQA, bus.MsgRequest, "tdd-generate"))
		if err := qa.GenerateTests(ctx, agent.QATestPayload{
			Plan:           plan,
			ProjectContext: ctxFragment,
			StageName:      title,
			Files:          collectProjectFilesFromRoot(nil, tr.root),
		}); err != nil {
			tr.event(fmt.Sprintf("warning: QA test generation: %v", err))
		}
		tr.b.Publish(bus.NewMessage(bus.RoleQA, "", bus.MsgResponse, "tests generated"))
	}

	// Step 2: Coder implements.
	tr.setState(PipelineCoding)
	if tr.codingStarted.IsZero() {
		tr.codingStarted = time.Now()
	}

	coderResp, err := tr.runAgent(ctx, bus.RoleCoder, agent.CoderPayload{
		Plan:           plan,
		ProjectContext: ctxFragment,
		StageName:      title,
		StageIndex:     1,
		TotalStages:    1,
	})
	if err != nil {
		return fmt.Errorf("coder: %w", err)
	}

	coderResult := extractCoderResult(coderResp)

	// Step 3: Build & test loop with QA escalation.
	coder, hasCoder := tr.agents[bus.RoleCoder].(*agent.CoderAgent)
	if !hasCoder {
		return nil
	}

	maxAttempts := defaultMaxCoderAttempts
	if tr.cfg.Project.MaxFixAttempts > 0 {
		maxAttempts = tr.cfg.Project.MaxFixAttempts
	}

	qaVerifyRound := 0
	for attempt := 0; ; attempt++ {
		tr.setState(PipelineCoding)
		tr.event("running build and tests…")
		fixed, buildErr := coder.BuildAndFix(ctx, coderResult.Files)
		if buildErr != nil {
			if !agent.IsBuildFixStuck(buildErr) {
				return fmt.Errorf("build: %w", buildErr)
			}
			// BuildFixStuck: fall through to QA escalation below.
			tr.event("build-fix loop stuck — escalating to QA")
		} else {
			coderResult.Files = fixed

			// Run tests to verify.
			files := collectProjectFilesFromRoot(coderResult.Files, tr.root)
			testPass := tr.runBuildTests(ctx, files)
			if testPass {
				tr.event("bead complete — tests pass")
				return nil
			}
		}

		if attempt > 0 && attempt%maxAttempts == 0 && hasQA {
			// Escalate to QA: verify whether tests are correct.
			if qaVerifyRound >= maxQAVerifyRounds {
				tr.event("QA verification rounds exhausted — accepting current state")
				return nil
			}
			qaVerifyRound++

			tr.setState(PipelineQATests)
			tr.event(fmt.Sprintf("coder failed %d times — asking QA to verify tests (round %d/%d)…",
				attempt, qaVerifyRound, maxQAVerifyRounds))

			testFiles := filterTestFiles(coderResult.Files)
			verifyResult, verifyErr := qa.VerifyTests(ctx, agent.QAVerifyTestsPayload{
				Failure:        "Coder failed to make tests pass after multiple attempts",
				Files:          coderResult.Files,
				TestFiles:      testFiles,
				ProjectContext: ctxFragment,
			})
			if verifyErr != nil {
				tr.event(fmt.Sprintf("warning: QA verify: %v", verifyErr))
				continue
			}

			if verifyResult.TestsOK {
				tr.event("QA confirms tests are correct — coder must try harder")
			} else {
				tr.event(fmt.Sprintf("QA updated %d test file(s)", len(verifyResult.UpdatedFiles)))
				coderResult.Files = mergeFileList(coderResult.Files, verifyResult.UpdatedFiles)
			}
		}

		// Re-run coder fix with failure context.
		tr.setState(PipelineCoding)
		fixResp, fixErr := tr.runAgent(ctx, bus.RoleCoder, agent.CoderFixPayload{
			Failure:        "Tests are failing. Fix the implementation to make them pass.",
			ProjectContext: ctxFragment,
			Files:          coderResult.Files,
			Targets:        tr.scopedCoderFixTargets(ctx),
		})
		if fixErr != nil {
			return fmt.Errorf("coder fix: %w", fixErr)
		}
		fixResult := extractCoderResult(fixResp)
		coderResult.Files = mergeFileList(coderResult.Files, fixResult.Files)
	}
}

// qualityReview runs QA review + optional UX + Security on all project files.
// Returns raw feedback strings (empty = passed).
func (tr *TaskRunner) qualityReview(ctx context.Context, ctxFragment string, files []string) (qaFeedback, uxFeedback, secFeedback string) {
	// Measurement-only: when scoped_context_shadow is enabled, derive per-file
	// symbol targets from the semantic index so review agents can log the
	// scoped-vs-whole token estimate. This does NOT change what reviewers see.
	reviewTargets := tr.scopedShadowTargets(ctx)

	// QA quality/logic review.
	tr.setState(PipelineQAReview)
	tr.event("QA reviewing code quality…")

	qaResp, err := tr.runAgent(ctx, bus.RoleQA, agent.QAReviewPayload{
		Files:          files,
		Root:           tr.root,
		ProjectContext: ctxFragment,
		Targets:        reviewTargets,
	})
	if err != nil {
		tr.event(fmt.Sprintf("warning: QA review: %v", err))
	} else if result, ok := qaResp.Payload.(agent.QAReviewResult); ok {
		tr.niceToHave["QA Review"] = append(tr.niceToHave["QA Review"], result.NiceToHave...)
		if !result.Approved && len(result.MustFix) > 0 {
			qaFeedback = strings.Join(result.MustFix, "\n")
		}
		if result.Unparsed {
			qaFeedback = result.RawOutput
		}
		if qaFeedback == "" {
			tr.event("QA review passed — no must-fix issues")
		}
	}

	// UX review (optional).
	if _, ok := tr.agents[bus.RoleUXReviewer]; ok {
		tr.setState(PipelineUXReviewing)
		tr.event("UX reviewing…")
		uxResp, uxErr := tr.runAgent(ctx, bus.RoleUXReviewer, agent.UXReviewerPayload{
			Files:   files,
			Root:    tr.root,
			Targets: reviewTargets,
		})
		if uxErr != nil {
			tr.event(fmt.Sprintf("warning: UX review: %v", uxErr))
		} else if result, ok := uxResp.Payload.(agent.UXReviewResult); ok {
			tr.niceToHave["UX/UI"] = append(tr.niceToHave["UX/UI"], result.NiceToHave...)
			if !result.Approved && len(result.MustFix) > 0 {
				uxFeedback = strings.Join(result.MustFix, "\n")
			}
			if result.Unparsed {
				uxFeedback = result.RawOutput
			}
			if uxFeedback == "" {
				tr.event("UX review passed")
			}
		}
	}

	// Security review (optional).
	if _, ok := tr.agents[bus.RoleSecurity]; ok {
		tr.setState(PipelineSecurity)
		tr.event("Security reviewing…")
		secResp, secErr := tr.runAgent(ctx, bus.RoleSecurity, agent.SecurityPayload{
			Files:   files,
			Root:    tr.root,
			Targets: reviewTargets,
		})
		if secErr != nil {
			tr.event(fmt.Sprintf("warning: Security review: %v", secErr))
		} else if result, ok := secResp.Payload.(agent.SecurityResult); ok {
			tr.niceToHave["Security"] = append(tr.niceToHave["Security"], result.NiceToHave...)
			if !result.Approved && len(result.MustFix) > 0 {
				secFeedback = strings.Join(result.MustFix, "\n")
			}
			if result.Unparsed {
				secFeedback = result.RawOutput
			}
			if secFeedback == "" {
				tr.event("Security review passed")
			}
		}
	}

	return
}

// scopedCoderFixTargets returns per-file symbol targets for ADDITIVE scoped
// related context in the coder fix loop. Returns nil (a no-op) unless
// scoped_context_coder_fix is enabled and the semantic index yields hits.
// Best-effort: any error or empty result yields nil.
func (tr *TaskRunner) scopedCoderFixTargets(ctx context.Context) map[string][]string {
	if !tr.cfg.Project.Context.ScopedContextCoderFix {
		return nil
	}
	k := tr.cfg.Project.Context.SemanticIndex.TopK
	if k <= 0 {
		k = 20
	}
	targets, err := tr.projCtx.SemanticSearchSymbols(ctx, tr.taskQuery, k)
	if err != nil {
		tr.event(fmt.Sprintf("scoped coder-fix context: semantic search: %v", err))
		return nil
	}
	return targets
}

// scopedShadowTargets returns per-file symbol targets from the semantic index
// for measurement-only scoped-context shadow logging during review. Returns nil
// (a no-op for agents) unless scoped_context_shadow is enabled and the semantic
// index yields hits. Best-effort: any error or empty result yields nil.
func (tr *TaskRunner) scopedShadowTargets(ctx context.Context) map[string][]string {
	if !tr.cfg.Project.Context.ScopedContextShadow {
		return nil
	}
	k := tr.cfg.Project.Context.SemanticIndex.TopK
	if k <= 0 {
		k = 20
	}
	targets, err := tr.projCtx.SemanticSearchSymbols(ctx, tr.taskQuery, k)
	if err != nil {
		tr.event(fmt.Sprintf("scoped-context shadow: semantic search: %v", err))
		return nil
	}
	return targets
}

// runBuildTests runs test commands and returns true if all pass.
func (tr *TaskRunner) runBuildTests(ctx context.Context, files []string) bool {
	qaResp, err := tr.runAgent(ctx, bus.RoleQA, agent.QATestPayload{
		Files: files,
	})
	if err != nil {
		tr.event(fmt.Sprintf("warning: run tests: %v", err))
		return false
	}
	report, ok := qaResp.Payload.(agent.TestReport)
	if !ok {
		return false
	}
	if report.Success {
		tr.event("all tests passed")
	}
	return report.Success
}

// filterTestFiles returns the subset of files matching *_test.go.
func filterTestFiles(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

func (tr *TaskRunner) runAgent(ctx context.Context, role bus.AgentRole, payload any) (bus.Message, error) {
	a, ok := tr.agents[role]
	if !ok {
		return bus.Message{}, fmt.Errorf("no agent for role %q", role)
	}

	if role == bus.RoleCoder && tr.codingStarted.IsZero() {
		tr.codingStarted = time.Now()
	}

	tr.log.Info("starting agent", slog.String("role", string(role)))
	msg := bus.NewMessage(bus.RoleSystem, role, bus.MsgRequest, payload)
	tr.b.Publish(msg)
	tr.b.Publish(bus.NewMessage(bus.RoleSystem, role, bus.MsgEvent,
		fmt.Sprintf("starting %s", role)))

	started := time.Now()
	resp, err := a.Run(ctx, msg)
	elapsed := time.Since(started)
	tr.agentDurations[role] += elapsed

	if err != nil {
		if errors.Is(err, runner.ErrRateLimited) {
			tr.setState(PipelineRateLimited)
			tr.b.Publish(bus.NewMessage(role, "", bus.MsgEvent,
				fmt.Sprintf("⛔ rate limit / quota hit: %v — pipeline stopped", err)))
		}
		tr.log.Error("agent failed",
			slog.String("role", string(role)),
			slog.String("error", err.Error()),
			slog.Duration("elapsed", elapsed),
		)
		tr.b.Publish(bus.NewMessage(role, "", bus.MsgEvent,
			fmt.Sprintf("error: %v", err)))
		return bus.Message{}, err
	}
	tr.log.Info("agent completed", slog.String("role", string(role)))
	tr.b.Publish(bus.NewMessage(role, "", bus.MsgResponse,
		fmt.Sprintf("%s complete", role)))
	return resp, nil
}

func (tr *TaskRunner) setState(s PipelineState) {
	tr.state = s
	tr.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, string(s)))
}

// bootstrapForTDD scaffolds the minimum project manifest required for the
// tester to write valid tests before any production code exists. Currently
// handles Go projects when the spec implies Go and no go.mod is present.
func (tr *TaskRunner) bootstrapForTDD(spec agent.TaskSpec) {
	if isProjectScaffolded(tr.root) {
		return
	}
	if !specImpliesGo(spec, tr.cfg.Project.Language) {
		return
	}
	module, err := bootstrapGoModule(tr.root, spec.Title)
	if err != nil {
		tr.event(fmt.Sprintf("warning: go.mod bootstrap: %v", err))
		return
	}
	if module != "" {
		tr.event(fmt.Sprintf("bootstrapped go.mod (module=%s) so tester can write tests first", module))
	}
}

// registerBead creates a durable Beads issue for the approved task. Returns
// the bead id (empty when `bd` is unavailable or fails).
func (tr *TaskRunner) registerBead(ctx context.Context, spec agent.TaskSpec) string {
	if !beads.Available() {
		return ""
	}
	desc := spec.Description
	if len(spec.AcceptanceCriteria) > 0 {
		desc += "\n\nAcceptance criteria:\n- " + strings.Join(spec.AcceptanceCriteria, "\n- ")
	}
	id, err := beads.Create(ctx, tr.root, spec.Title, desc, 2)
	if err != nil {
		tr.event(fmt.Sprintf("warning: bd create: %v", err))
		return ""
	}
	if err := beads.Claim(ctx, tr.root, id); err != nil {
		tr.event(fmt.Sprintf("warning: bd claim %s: %v", id, err))
	}
	tr.event(fmt.Sprintf("registered task in beads as %s", id))
	return id
}

// closeBead marks the Beads issue as done. No-op when id is empty.
func (tr *TaskRunner) closeBead(ctx context.Context, id, reason string) {
	if id == "" {
		return
	}
	if err := beads.Close(ctx, tr.root, id, reason); err != nil {
		tr.event(fmt.Sprintf("warning: bd close %s: %v", id, err))
		return
	}
	tr.event(fmt.Sprintf("closed beads issue %s", id))
}

func (tr *TaskRunner) event(msg string) {
	tr.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, msg))
}

// waitArtifact blocks until the user approves or asks to regenerate the artifact.
// Returns true on approval, false on regenerate request.
func (tr *TaskRunner) waitArtifact(ctx context.Context, filename string) (bool, error) {
	tr.log.Info("waiting for artifact approval", slog.String("artifact", filename))
	tr.setState(PipelineGate)
	tr.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgHumanGate, filename))
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-tr.regenerateCh:
		tr.log.Info("artifact regeneration requested", slog.String("artifact", filename))
		return false, nil
	case <-tr.gateCh:
		tr.log.Info("artifact approved", slog.String("artifact", filename))
		return true, nil
	}
}

// ResumeBuildFix re-runs the coder's build-and-fix loop using the current project
// files on disk. Use after a build-fix failure to give the coder another round
// of attempts without restarting the whole task pipeline.
func (tr *TaskRunner) ResumeBuildFix(ctx context.Context) error {
	coder, ok := tr.agents[bus.RoleCoder].(*agent.CoderAgent)
	if !ok {
		return fmt.Errorf("no coder agent available to resume")
	}
	tr.setState(PipelineCoding)
	tr.event("resuming build-and-fix loop…")
	files := collectProjectFilesFromRoot(nil, tr.root)
	if _, err := coder.BuildAndFix(ctx, files); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	tr.setState(PipelineDone)
	return nil
}

func removeWorkspaceFile(ws artifacts.Workspace, name string) error {
	return os.Remove(ws.Path(name))
}
