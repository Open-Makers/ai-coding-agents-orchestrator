package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/beads"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	appctx "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/gitclient"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/notify"
	appprompts "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
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

	// startedAt marks the start of the whole run, for the total wall-clock stat.
	startedAt time.Time

	// Run statistics surfaced in the final summary.
	statsMu     sync.Mutex
	usageByRole map[bus.AgentRole]bus.AgentUsage // token usage accumulated from the bus
	touched     map[string]bool                  // distinct files written by agents
	subTaskCount int                             // sub-tasks in the approved plan
	fixRounds    int                             // quality back-and-forth rounds

	// projCtx is the project context collected at the start of a run; reused
	// for scoped-context shadow measurement during review. Best-effort.
	projCtx   appctx.ProjectContext
	taskQuery string // task description used as the semantic-search query

	// taskBeadID is the top-level orchestrator-task bead for this run; sub-task
	// beads are linked as its children. runID identifies the run for resume
	// detection (stored as bead metadata and in task_spec.json).
	taskBeadID string
	runID      string

	// loadedModel tracks the local model currently resident in memory so the
	// previous one can be unloaded when the pipeline switches agent/model,
	// freeing RAM before the next model loads. loadedSet guards the zero value.
	loadedModel config.AgentConfig
	loadedSet   bool

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
		usageByRole:    make(map[bus.AgentRole]bus.AgentUsage),
		touched:        make(map[string]bool),
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
	err := tr.run(ctx, taskInput, false)
	if err != nil {
		if errors.Is(err, runner.ErrRateLimited) && tr.state != PipelineRateLimited {
			tr.setState(PipelineRateLimited)
			tr.event(fmt.Sprintf("⛔ rate limit / quota hit: %v — pipeline stopped", err))
			tr.notify("Rate limited", "Quota hit — pipeline stopped")
		} else {
			tr.notify("Error", fmt.Sprintf("Pipeline failed: %v", err))
		}
	} else {
		tr.notify("Complete", "Pipeline finished — review artifacts in .orchestrator/")
	}
	return err
}

// Resume continues an interrupted task: it loads the top-level bead, spec and
// sub-task plan from the workspace, skips negotiate/decompose, and finishes the
// remaining (open or interrupted) sub-tasks. Use Resumable() to check first.
func (tr *TaskRunner) Resume(ctx context.Context) error {
	err := tr.run(ctx, "", true)
	if err != nil {
		if errors.Is(err, runner.ErrRateLimited) && tr.state != PipelineRateLimited {
			tr.setState(PipelineRateLimited)
			tr.event(fmt.Sprintf("⛔ rate limit / quota hit: %v — pipeline stopped", err))
			tr.notify("Rate limited", "Quota hit — pipeline stopped")
		} else {
			tr.notify("Error", fmt.Sprintf("Pipeline failed: %v", err))
		}
	} else {
		tr.notify("Complete", "Pipeline finished — review artifacts in .orchestrator/")
	}
	return err
}

func (tr *TaskRunner) run(ctx context.Context, taskInput string, resume bool) (retErr error) {
	promptsDir := filepath.Join(tr.root, artifacts.DirName, appprompts.PromptsDirName)
	appprompts.SetOverrideDir(promptsDir)
	appprompts.SetUserLanguage("English")

	tr.startedAt = time.Now()
	stopUsage := tr.startUsageAccumulator()
	defer stopUsage()

	var (
		curStage = "init"
		curSpec  agent.TaskSpec
	)
	curSpec.Title = strings.TrimSpace(firstLine(taskInput))
	if !resume && tr.runID == "" {
		tr.runID = newRunID()
	}
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

	var (
		spec       agent.TaskSpec
		subTasks   []agent.SubTask
		subBeadIDs map[string]string
		beadID     string
	)

	// On resume, load the saved state up front so memory recall and the
	// semantic query use the actual task (taskInput is empty for resume).
	memoryQuery := strings.TrimSpace(taskInput)
	if resume {
		curStage = "resume-load"
		s, st, plan, lerr := tr.loadResumeState()
		if lerr != nil {
			return lerr
		}
		spec, curSpec = s, s
		tr.taskBeadID, tr.runID, beadID = st.TopBeadID, st.RunID, st.TopBeadID
		subTasks, subBeadIDs = plan.Tasks, plan.IDByKey
		memoryQuery = strings.TrimSpace(spec.Title + " " + spec.Description)
	}
	tr.taskQuery = memoryQuery

	if recalled, mErr := appctx.CollectMemory(ctx, tr.root, tr.cfg, memoryQuery); mErr != nil {
		tr.event(fmt.Sprintf("memory recall warning: %v", mErr))
		projCtx.Memory = recalled
	} else {
		projCtx.Memory = recalled
		if len(recalled.Hits) > 0 || recalled.Pinned != "" {
			tr.event(fmt.Sprintf("memory: recalled %d fragment(s) from past work", len(recalled.Hits)))
		}
	}

	ctxFragment := projCtx.SystemPromptFragment(appctx.ProfileFull)

	if resume {
		curStage = "execute"
		tr.event(fmt.Sprintf("resuming task %s: %s", beadID, spec.Title))
		// Resume safety: re-run build/tests to establish the current state and
		// give agents the work already in progress (git diff), so they continue
		// from the partial state instead of restarting from scratch.
		if rc := tr.buildResumeContext(ctx); rc != "" {
			ctxFragment += "\n\n" + rc
		}
	} else {
		tr.memAppend("task-start", fmt.Sprintf("**Task input:**\n\n%s", strings.TrimSpace(taskInput)))

		// ── Phase 1: Negotiate ── (human in the loop)
		curStage = "negotiate"
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

		beadID = tr.registerBead(ctx, spec)

		// ── Phase 2: Decompose ── (PM creates sub-tasks / beads)
		curStage = "decompose"
		subTasks, err = tr.runDecomposeGate(ctx, spec, "", ctxFragment, projCtx.IsBrownfield)
		if err != nil {
			return fmt.Errorf("decompose: %w", err)
		}
		tr.memAppend("plan-done", fmt.Sprintf("Decomposed into %d sub-task(s).", len(subTasks)))

		subBeadIDs = tr.createSubTaskBeads(ctx, subTasks)
		// Persist the structured plan so an interrupted run can resume without
		// re-negotiating or re-decomposing.
		if err := tr.writeSubTasks(subTasks, subBeadIDs); err != nil {
			tr.event(fmt.Sprintf("warning: write sub-task plan: %v", err))
		}
		// Persist the run-state sidecar so the run can be paused/resumed. This
		// is written unconditionally — even when bd is unavailable and no top
		// bead was registered — so resume never depends on a working bd.
		if err := tr.writeRunState(spec.Title); err != nil {
			tr.event(fmt.Sprintf("warning: write run state: %v", err))
		}
	}

	// ── Phase 3+4: Implement + Quality Review loop ──
	curStage = "execute"
	tr.subTaskCount = len(subTasks)
	tr.bootstrapForTDD(spec)

	if err := tr.implementAndReview(ctx, spec, ctxFragment, subTasks, subBeadIDs); err != nil {
		return err
	}

	curStage = "finalise"
	saveNiceToHaveFile(tr.ws, tr.niceToHave, tr.log)
	tr.setState(PipelineDone)
	emitSummary(tr.b, tr.ws, tr.agents, tr.niceToHave, tr.collectStats())
	tr.finaliseMemory(ctx, spec)
	tr.closeBead(ctx, beadID, "Completed by orchestrator")
	// The run is complete; drop the resume sidecars so it isn't offered later.
	_ = removeWorkspaceFile(tr.ws, artifacts.RunStateFile)
	_ = removeWorkspaceFile(tr.ws, artifacts.SubTasksFile)
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
		tr.fixRounds++

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

		// Security + Quality review the plan BEFORE the human approval gate, so
		// the human sees reviewer concerns alongside the plan.
		planMD := renderTaskPlan(tasks)
		reviewNotes := tr.reviewPlan(ctx, planMD, ctxFragment)
		if err := tr.ws.WriteFile(artifacts.TaskPlanFile, []byte(planMD+reviewNotes)); err != nil {
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

// reviewPlan asks the Security and QA agents to audit the implementation plan
// before the human approves it. It returns a markdown "Reviewer Notes" section
// (empty when both reviewers are absent or have no concerns) to append to the
// task plan shown at the approval gate. Best-effort: reviewer errors are logged
// and do not block the gate.
func (tr *TaskRunner) reviewPlan(ctx context.Context, planMD, ctxFragment string) string {
	var sb strings.Builder

	if sec, ok := tr.agents[bus.RoleSecurity].(*agent.SecurityAgent); ok {
		tr.setState(PipelineSecurity)
		tr.event("Security reviewing the plan…")
		if res, err := sec.ReviewPlan(ctx, planMD, ctxFragment); err != nil {
			tr.event(fmt.Sprintf("warning: security plan review: %v", err))
		} else if len(res.MustFix) > 0 {
			sb.WriteString("\n### Security\n\n")
			for _, item := range res.MustFix {
				fmt.Fprintf(&sb, "- %s\n", item)
			}
			tr.event(fmt.Sprintf("Security raised %d plan concern(s)", len(res.MustFix)))
		} else {
			tr.event("Security plan review: no concerns")
		}
	}

	if qa, ok := tr.agents[bus.RoleQA].(*agent.QAAgent); ok {
		tr.setState(PipelineQAReview)
		tr.event("QA reviewing the plan…")
		if res, err := qa.ReviewPlan(ctx, planMD, ctxFragment); err != nil {
			tr.event(fmt.Sprintf("warning: qa plan review: %v", err))
		} else if len(res.MustFix) > 0 {
			sb.WriteString("\n### Quality\n\n")
			for _, item := range res.MustFix {
				fmt.Fprintf(&sb, "- %s\n", item)
			}
			tr.event(fmt.Sprintf("QA raised %d plan concern(s)", len(res.MustFix)))
		} else {
			tr.event("QA plan review: no concerns")
		}
	}

	if sb.Len() == 0 {
		return ""
	}
	return "\n---\n\n## Reviewer Notes\n\nRaised by Security & Quality before approval — consider addressing or accept as-is.\n" + sb.String()
}

// resumeDiffMaxTokens caps the size of the uncommitted-changes diff injected
// into the resume context so a large diff doesn't blow the prompt budget.
const resumeDiffMaxTokens = 2000

// buildResumeContext produces a context block describing the partial state of an
// interrupted task: the current uncommitted changes and whether the project
// currently builds/tests cleanly. It is prepended to the agent context on
// resume so agents continue from the existing work rather than restarting.
func (tr *TaskRunner) buildResumeContext(ctx context.Context) string {
	var sb strings.Builder
	sb.WriteString("## Resuming an interrupted task\n\n")
	sb.WriteString("This task was interrupted; the workspace already contains partial work. Continue from the current state — do NOT restart from scratch or duplicate existing changes.\n\n")

	gc := gitclient.New(tr.root)
	if diff, err := gc.Diff("."); err == nil {
		if diff = strings.TrimSpace(diff); diff != "" {
			sb.WriteString("### Uncommitted changes so far\n```diff\n")
			sb.WriteString(tokenutil.Truncate(diff, resumeDiffMaxTokens))
			sb.WriteString("\n```\n\n")
		}
	}

	tr.event("resume: checking current build/test state…")
	files := collectProjectFilesFromRoot(nil, tr.root)
	if tr.runBuildTests(ctx, files) {
		sb.WriteString("### Current build/tests: PASSING. Verify each remaining sub-task is genuinely incomplete before changing more code.\n")
	} else {
		sb.WriteString("### Current build/tests: FAILING. Fixing these failures is part of the remaining work.\n")
	}
	return sb.String()
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
	// Pass 1: create all sub-task beads and link them to the top-level task.
	for _, t := range tasks {
		id, err := beads.Create(ctx, tr.root, t.Title, t.Description, t.Priority, labelOrchestratorSubtask)
		if err != nil {
			tr.event(fmt.Sprintf("warning: bd create %s: %v", t.Key, err))
			continue
		}
		idByKey[t.Key] = id
		// Link the sub-task as a child of the top-level task bead so the task's
		// sub-tasks can be listed and execution can be scoped with --parent.
		if tr.taskBeadID != "" {
			if err := beads.Link(ctx, tr.root, id, tr.taskBeadID, "parent-child"); err != nil {
				tr.event(fmt.Sprintf("warning: bd link %s→%s (parent-child): %v", id, tr.taskBeadID, err))
			}
		}
		tr.event(fmt.Sprintf("registered %s as %s — %s", t.Key, id, t.Title))
	}
	// Pass 2: wire sibling dependencies now that all ids are known, so a
	// dependency on a later-declared sub-task is not silently dropped.
	for _, t := range tasks {
		id, ok := idByKey[t.Key]
		if !ok {
			continue
		}
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
	}
	return idByKey
}

// executeBeads drives implementation by polling the task's ready sub-task beads,
// claiming them, and running a QA-tests → Coder loop per bead. Interrupted
// (claimed-but-open) sub-tasks are resumed first, then newly-ready ones.
// Execution is scoped to the top-level task bead so other tasks' beads are not
// interleaved. Falls back to in-order execution when `bd` is unavailable.
func (tr *TaskRunner) executeBeads(ctx context.Context, spec agent.TaskSpec, ctxFragment string, tasks []agent.SubTask, idByKey map[string]string) error {
	if len(idByKey) == 0 {
		// No beads were created (bd unavailable at decompose) — run in order.
		return tr.executeSubTasksInOrder(ctx, spec, ctxFragment, tasks)
	}
	if !beads.Available() {
		// The plan is bead-backed but bd is no longer available. Running
		// in-order here would re-run already-completed sub-tasks (their done
		// state lives in beads), so refuse instead of corrupting progress.
		return fmt.Errorf("this task is tracked in beads but `bd` is not available — install bd to continue/resume")
	}

	// Resolve a bead's title/description from the decomposed sub-tasks; `bd
	// ready` does not return descriptions, so without this lookup the coder
	// would receive only the title.
	subByID := make(map[string]agent.SubTask, len(tasks))
	for _, t := range tasks {
		if id, ok := idByKey[t.Key]; ok {
			subByID[id] = t
		}
	}
	resolve := func(b beads.Issue) (title, desc string) {
		if st, ok := subByID[b.ID]; ok {
			return st.Title, st.Description
		}
		return b.Title, ""
	}

	// ── Phase A: QA writes tests for ALL sub-tasks first (TDD across the whole
	// plan) before any implementation begins. ──
	tr.generateAllTests(ctx, ctxFragment, tasks)

	// ── Phase B: Coder implements ALL sub-tasks (bead-ordered), accumulating
	// the produced files. No per-bead build/fix here — verification is global. ──
	var implFiles []string
	total := len(tasks)
	done := 0
	processed := make(map[string]bool)
	for {
		bead, resumed, ok, err := tr.nextBead(ctx, processed)
		if err != nil {
			return fmt.Errorf("select next sub-task: %w", err)
		}
		if !ok {
			break
		}

		done++
		title, desc := resolve(bead)
		if resumed {
			tr.event(fmt.Sprintf("── Sub-task %d/%d (%s, resumed): %s ──", done, total, bead.ID, title))
		} else {
			tr.event(fmt.Sprintf("── Sub-task %d/%d (%s): %s ──", done, total, bead.ID, title))
		}

		files, err := tr.implementBead(ctx, ctxFragment, title, desc)
		if err != nil {
			return fmt.Errorf("bead %s: %w", bead.ID, err)
		}
		implFiles = mergeFileList(implFiles, files)
		processed[bead.ID] = true
		if err := beads.Close(ctx, tr.root, bead.ID, "Completed by orchestrator"); err != nil {
			// A failed close would leave the sub-task in_progress; don't proceed
			// to finalize (which removes resume state) on an inconsistent bead.
			return fmt.Errorf("close sub-task %s: %w", bead.ID, err)
		}
	}

	// Confirm everything is actually closed before declaring implementation
	// done — otherwise a blocked/cyclic/orphaned child would be silently
	// dropped and the resume sidecars removed.
	if err := tr.verifySubtasksComplete(ctx); err != nil {
		return err
	}

	// ── Phase C: one global build → test → fix loop over the whole project,
	// only now that every stage is implemented. ──
	return tr.globalBuildFix(ctx, ctxFragment, implFiles)
}

// verifySubtasksComplete returns an error if any child of the task bead is not
// closed, so executeBeads never reports success while work remains. Best-effort
// when the parent is unknown or the listing fails.
func (tr *TaskRunner) verifySubtasksComplete(ctx context.Context) error {
	if tr.taskBeadID == "" {
		return nil
	}
	remaining, err := beads.Children(ctx, tr.root, tr.taskBeadID, "open", "in_progress", "blocked")
	if err != nil {
		return nil // best-effort: don't block finalize on a transient list error
	}
	if len(remaining) == 0 {
		return nil
	}
	ids := make([]string, 0, len(remaining))
	for _, r := range remaining {
		ids = append(ids, r.ID)
	}
	return fmt.Errorf("%d sub-task(s) still unresolved (%s) — cannot complete; check dependencies/blockers in beads",
		len(remaining), strings.Join(ids, ", "))
}

// nextBead selects the next sub-task bead to run for the current task: an
// interrupted (in_progress) child first, otherwise the next ready child (which
// it claims). processed guards against an in_progress bead that fails to close,
// which would otherwise loop forever. ok is false when no work remains.
func (tr *TaskRunner) nextBead(ctx context.Context, processed map[string]bool) (bead beads.Issue, resumed, ok bool, err error) {
	if tr.taskBeadID != "" {
		inprog, lerr := beads.Children(ctx, tr.root, tr.taskBeadID, "in_progress")
		if lerr == nil && len(inprog) > 0 {
			if ip, found := pickInProgress(inprog, processed); found {
				return ip, true, true, nil
			}
			// All in_progress beads were already executed but did not close.
			tr.event("warning: in_progress sub-task(s) did not close after execution — stopping to avoid a loop")
			return beads.Issue{}, false, false, nil
		}
	}

	ready, rerr := beads.Ready(ctx, tr.root, tr.taskBeadID)
	if rerr != nil {
		return beads.Issue{}, false, false, fmt.Errorf("bd ready: %w", rerr)
	}
	if len(ready) == 0 {
		return beads.Issue{}, false, false, nil
	}
	bead = ready[0]
	if cerr := beads.Claim(ctx, tr.root, bead.ID); cerr != nil {
		tr.event(fmt.Sprintf("warning: bd claim %s: %v", bead.ID, cerr))
	}
	return bead, false, true, nil
}

// pickInProgress returns the first in_progress bead not already processed this
// run. found is false when every in_progress bead was already executed (which
// means a close failed) — the caller must stop to avoid an infinite loop.
func pickInProgress(inprog []beads.Issue, processed map[string]bool) (beads.Issue, bool) {
	for _, ip := range inprog {
		if !processed[ip.ID] {
			return ip, true
		}
	}
	return beads.Issue{}, false
}

// executeSubTasksInOrder runs sub-tasks sequentially when `bd` is unavailable,
// using the same phased flow as the bead-backed path: all tests first, then all
// implementations, then one global build/fix loop.
func (tr *TaskRunner) executeSubTasksInOrder(ctx context.Context, spec agent.TaskSpec, ctxFragment string, tasks []agent.SubTask) error {
	_ = spec
	tr.generateAllTests(ctx, ctxFragment, tasks)

	var implFiles []string
	for i, t := range tasks {
		tr.event(fmt.Sprintf("── Sub-task %d/%d: %s ──", i+1, len(tasks), t.Title))
		files, err := tr.implementBead(ctx, ctxFragment, t.Title, t.Description)
		if err != nil {
			return fmt.Errorf("sub-task %s: %w", t.Key, err)
		}
		implFiles = mergeFileList(implFiles, files)
	}
	return tr.globalBuildFix(ctx, ctxFragment, implFiles)
}

// generateAllTests asks QA to write tests (TDD) for every sub-task up front,
// before any implementation. Best-effort: a failure on one stage is logged and
// the remaining stages still get tests. No-op when there is no QA agent.
func (tr *TaskRunner) generateAllTests(ctx context.Context, ctxFragment string, tasks []agent.SubTask) {
	qa, hasQA := tr.agents[bus.RoleQA].(*agent.QAAgent)
	if !hasQA {
		return
	}
	tr.setState(PipelineQATests)
	total := len(tasks)
	for i, t := range tasks {
		tr.event(fmt.Sprintf("── Tests %d/%d: %s ──", i+1, total, t.Title))
		tr.event("QA writing tests (TDD)…")
		tr.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RoleQA, bus.MsgRequest, "tdd-generate"))
		if err := qa.GenerateTests(ctx, agent.QATestPayload{
			Plan:           t.Title + "\n\n" + t.Description,
			ProjectContext: ctxFragment,
			StageName:      t.Title,
			Files:          collectProjectFilesFromRoot(nil, tr.root),
		}); err != nil {
			tr.event(fmt.Sprintf("warning: QA test generation (%s): %v", t.Title, err))
		}
		tr.b.Publish(bus.NewMessage(bus.RoleQA, "", bus.MsgResponse, "tests generated"))
	}
}

// implementBead runs the coder once for a single sub-task and returns the files
// it produced. Tests are written separately (generateAllTests) and verification
// happens globally (globalBuildFix), so this performs no build/fix loop.
func (tr *TaskRunner) implementBead(ctx context.Context, ctxFragment, title, description string) ([]string, error) {
	tr.setState(PipelineCoding)
	if tr.codingStarted.IsZero() {
		tr.codingStarted = time.Now()
	}
	resp, err := tr.runAgent(ctx, bus.RoleCoder, agent.CoderPayload{
		Plan:           title + "\n\n" + description,
		ProjectContext: ctxFragment,
		StageName:      title,
		StageIndex:     1,
		TotalStages:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("coder: %w", err)
	}
	files := extractCoderResult(resp).Files
	tr.markTouched(files)
	return files, nil
}

// globalBuildFix runs one project-wide build → test → fix loop after all stages
// are implemented. It uses the coder's build-fix loop and escalates to QA when
// the coder repeatedly fails, exactly as the per-bead loop used to, but over the
// whole accumulated file set instead of a single stage.
func (tr *TaskRunner) globalBuildFix(ctx context.Context, ctxFragment string, files []string) error {
	coder, hasCoder := tr.agents[bus.RoleCoder].(*agent.CoderAgent)
	if !hasCoder {
		return nil
	}
	qa, hasQA := tr.agents[bus.RoleQA].(*agent.QAAgent)

	maxAttempts := defaultMaxCoderAttempts
	if tr.cfg.Project.MaxFixAttempts > 0 {
		maxAttempts = tr.cfg.Project.MaxFixAttempts
	}

	qaVerifyRound := 0
	for attempt := 0; ; attempt++ {
		tr.setState(PipelineCoding)
		tr.event("running build and tests…")
		fixed, buildErr := coder.BuildAndFix(ctx, files)
		if buildErr != nil {
			if !agent.IsBuildFixStuck(buildErr) {
				return fmt.Errorf("build: %w", buildErr)
			}
			// BuildFixStuck: fall through to QA escalation below.
			tr.event("build-fix loop stuck — escalating to QA")
		} else {
			files = fixed

			// Run tests to verify the whole project.
			projFiles := collectProjectFilesFromRoot(files, tr.root)
			if tr.runBuildTests(ctx, projFiles) {
				tr.event("implementation complete — tests pass")
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

			testFiles := filterTestFiles(files)
			verifyResult, verifyErr := qa.VerifyTests(ctx, agent.QAVerifyTestsPayload{
				Failure:        "Coder failed to make tests pass after multiple attempts",
				Files:          files,
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
				files = mergeFileList(files, verifyResult.UpdatedFiles)
			}
		}

		// Re-run coder fix with failure context.
		tr.setState(PipelineCoding)
		fixResp, fixErr := tr.runAgent(ctx, bus.RoleCoder, agent.CoderFixPayload{
			Failure:        "Tests are failing. Fix the implementation to make them pass.",
			ProjectContext: ctxFragment,
			Files:          files,
			Targets:        tr.scopedCoderFixTargets(ctx),
		})
		if fixErr != nil {
			return fmt.Errorf("coder fix: %w", fixErr)
		}
		fixResult := extractCoderResult(fixResp)
		files = mergeFileList(files, fixResult.Files)
		tr.markTouched(fixResult.Files)
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

	tr.unloadOnSwitch(ctx, role)

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

// unloadOnSwitch frees the memory held by the previously-active local model
// when the pipeline moves to an agent whose runner/model differs. This prevents
// multiple local models from accumulating in RAM across pipeline stages. Cloud
// runners hold no local RAM, so switching to/from them only triggers an unload
// of a prior *local* model. Best-effort: unload failures are logged, not fatal.
func (tr *TaskRunner) unloadOnSwitch(ctx context.Context, role bus.AgentRole) {
	next := tr.cfg.Agents[string(role)]
	if tr.loadedSet && shouldUnloadPrev(tr.loadedModel, next) {
		prev := tr.loadedModel
		tr.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent,
			fmt.Sprintf("unloading %s model %q to free RAM before %s",
				prev.Runner, prev.Model, role)))
		if err := runner.UnloadFor(ctx, prev); err != nil {
			tr.log.Warn("model unload failed",
				slog.String("runner", prev.Runner),
				slog.String("model", prev.Model),
				slog.String("error", err.Error()),
			)
		}
	}
	tr.loadedModel = next
	tr.loadedSet = true
}

// shouldUnloadPrev reports whether the previously-loaded model should be
// unloaded before switching to next: only when it is a local model (cloud holds
// no local RAM) and the runner or model actually changed.
func shouldUnloadPrev(prev, next config.AgentConfig) bool {
	if !runner.IsLocalRunner(prev) {
		return false
	}
	return prev.Runner != next.Runner || prev.Model != next.Model
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

// Beads labels used to identify orchestrator-managed work for resume detection.
const (
	labelOrchestratorTask    = "orchestrator-task"
	labelOrchestratorSubtask = "orchestrator-subtask"
)

// newRunID returns a short random identifier for a pipeline run, used to tie a
// top-level bead to its workspace artifacts for resume detection.
func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// registerBead creates a durable Beads issue for the approved task. Returns
// the bead id (empty when `bd` is unavailable or fails). The top-level bead is
// labelled orchestrator-task and tagged with the run id so an interrupted task
// can be found and resumed.
func (tr *TaskRunner) registerBead(ctx context.Context, spec agent.TaskSpec) string {
	if !beads.Available() {
		return ""
	}
	desc := spec.Description
	if len(spec.AcceptanceCriteria) > 0 {
		desc += "\n\nAcceptance criteria:\n- " + strings.Join(spec.AcceptanceCriteria, "\n- ")
	}
	id, err := beads.Create(ctx, tr.root, spec.Title, desc, 2, labelOrchestratorTask)
	if err != nil {
		tr.event(fmt.Sprintf("warning: bd create: %v", err))
		return ""
	}
	if tr.runID != "" {
		if err := beads.SetMetadata(ctx, tr.root, id, map[string]string{"run_id": tr.runID}); err != nil {
			tr.event(fmt.Sprintf("warning: bd set-metadata %s: %v", id, err))
		}
	}
	if err := beads.Claim(ctx, tr.root, id); err != nil {
		tr.event(fmt.Sprintf("warning: bd claim %s: %v", id, err))
	}
	tr.taskBeadID = id
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

// startUsageAccumulator subscribes to the bus and accumulates per-agent token
// usage into usageByRole until the returned stop function is called. Used to
// surface token statistics in the final summary. Best-effort: a few in-flight
// messages may be missed at shutdown, which is acceptable for a summary.
func (tr *TaskRunner) startUsageAccumulator() func() {
	sub := tr.b.Subscribe()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case msg, ok := <-sub:
				if !ok {
					return
				}
				if msg.Type != bus.MsgUsage {
					continue
				}
				u, ok := msg.Payload.(bus.AgentUsage)
				if !ok {
					continue
				}
				tr.statsMu.Lock()
				cur := tr.usageByRole[msg.From]
				cur.InputTokens += u.InputTokens
				cur.OutputTokens += u.OutputTokens
				if u.Estimated {
					cur.Estimated = true
				}
				tr.usageByRole[msg.From] = cur
				tr.statsMu.Unlock()
			}
		}
	}()
	return func() { close(stop) }
}

// markTouched records files written by agents so the summary can report how
// many distinct files the run produced or changed.
func (tr *TaskRunner) markTouched(files []string) {
	if len(files) == 0 {
		return
	}
	tr.statsMu.Lock()
	for _, f := range files {
		if f != "" {
			tr.touched[f] = true
		}
	}
	tr.statsMu.Unlock()
}

// collectStats snapshots the accumulated run statistics for the final summary.
func (tr *TaskRunner) collectStats() summaryStats {
	tr.statsMu.Lock()
	defer tr.statsMu.Unlock()
	usage := make(map[bus.AgentRole]bus.AgentUsage, len(tr.usageByRole))
	for k, v := range tr.usageByRole {
		usage[k] = v
	}
	return summaryStats{
		startedAt:      tr.startedAt,
		codingStarted:  tr.codingStarted,
		agentDurations: tr.agentDurations,
		usageByRole:    usage,
		subTasks:       tr.subTaskCount,
		fixRounds:      tr.fixRounds,
		filesTouched:   len(tr.touched),
		niceToHave:     totalNiceToHave(tr.niceToHave),
	}
}

// notify sends a best-effort external alert (cmux) for a noteworthy pipeline
// moment — approval needed, completion, or failure — mirroring how Claude Code
// forwards alerts into cmux. No-ops when notifications are disabled or cmux is
// not running.
func (tr *TaskRunner) notify(subtitle, body string) {
	if tr.cfg.Notifications.DisableCMux {
		return
	}
	notify.SendCMux("Orchestrator", subtitle, body)
}

// waitArtifact blocks until the user approves or asks to regenerate the artifact.
// Returns true on approval, false on regenerate request.
func (tr *TaskRunner) waitArtifact(ctx context.Context, filename string) (bool, error) {
	tr.log.Info("waiting for artifact approval", slog.String("artifact", filename))
	tr.setState(PipelineGate)
	tr.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgHumanGate, filename))
	tr.notify("Approval needed", fmt.Sprintf("Review %s and approve to continue", filename))
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
