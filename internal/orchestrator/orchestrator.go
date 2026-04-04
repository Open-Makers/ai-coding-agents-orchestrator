package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	appctx "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/context"
)

const maxFixAttempts = 3

// PipelineState represents the current phase of the pipeline.
type PipelineState string

const (
	PipelineIdle      PipelineState = "idle"
	PipelinePlanning  PipelineState = "planning"
	PipelineCoding    PipelineState = "coding"
	PipelineTesting   PipelineState = "testing"
	PipelineReviewing PipelineState = "reviewing"
	PipelineFixing    PipelineState = "fixing"
	PipelineDone      PipelineState = "done"
	PipelineGate      PipelineState = "human_gate"
)

// Pipeline is the event-driven orchestrator driving agents through the workflow.
type Pipeline struct {
	b      *bus.Bus
	agents map[bus.AgentRole]agent.Agent
	cfg    config.Config
	ws     artifacts.Workspace
	root   string
	state  PipelineState

	// gateCh receives a signal when a human gate is programmatically approved.
	gateCh chan struct{}
}

func NewPipeline(
	b *bus.Bus,
	agents map[bus.AgentRole]agent.Agent,
	cfg config.Config,
	ws artifacts.Workspace,
	root string,
) *Pipeline {
	return &Pipeline{
		b:      b,
		agents: agents,
		cfg:    cfg,
		ws:     ws,
		root:   root,
		state:  PipelineIdle,
		gateCh: make(chan struct{}, 1),
	}
}

// Approve unblocks the current human gate programmatically (used by TUI).
func (p *Pipeline) Approve() {
	select {
	case p.gateCh <- struct{}{}:
	default:
	}
}

// CurrentState returns the current pipeline state.
func (p *Pipeline) CurrentState() PipelineState { return p.state }

// Run executes the full pipeline: PLAN → CODE → TEST → REVIEW → FIX* → DONE.
func (p *Pipeline) Run(ctx context.Context, requirementsPath string) error {
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

	// ── PLAN ──
	p.setState(PipelinePlanning)
	_, err = p.runAgent(ctx, bus.RolePlanner, agent.PlannerPayload{
		Requirements:   string(reqs),
		ProjectContext: ctxFragment,
	})
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	if err := p.waitApproval(ctx, artifacts.ArchitectureApprovedFile,
		"Review architecture.md then: orchestrator approve architecture"); err != nil {
		return err
	}
	if err := p.waitApproval(ctx, artifacts.PlanApprovedFile,
		"Review implementation_plan.md then: orchestrator approve plan"); err != nil {
		return err
	}
	if err := p.waitApproval(ctx, artifacts.PromptsApprovedFile,
		"Review prompts.md then: orchestrator approve prompts"); err != nil {
		return err
	}

	// ── CODE ──
	p.setState(PipelineCoding)
	planData, _ := p.ws.ReadFile(artifacts.ImplementationPlanFile)
	if _, err := p.runAgent(ctx, bus.RoleCoder, agent.CoderPayload{
		Plan:           string(planData),
		ProjectContext: ctxFragment,
	}); err != nil {
		return fmt.Errorf("code: %w", err)
	}

	if err := p.applyPatch(); err != nil {
		return fmt.Errorf("apply patch: %w", err)
	}

	// ── TEST + REVIEW → FIX LOOP ──
	for attempt := range maxFixAttempts + 1 {
		testOK, reviewOK, failure, err := p.testAndReview(ctx)
		if err != nil {
			return err
		}
		if testOK && reviewOK {
			return p.pr(ctx, string(reqs))
		}
		if attempt == maxFixAttempts {
			break
		}

		p.setState(PipelineFixing)
		p.event(fmt.Sprintf("fix attempt %d/%d", attempt+1, maxFixAttempts))
		if _, err := p.runAgent(ctx, bus.RoleFixer, agent.FixerPayload{Failure: failure}); err != nil {
			return fmt.Errorf("fix: %w", err)
		}
		if err := p.applyPatch(); err != nil {
			return fmt.Errorf("apply fix patch: %w", err)
		}
	}

	return fmt.Errorf("exceeded max fix attempts (%d)", maxFixAttempts)
}

func (p *Pipeline) testAndReview(ctx context.Context) (testOK, reviewOK bool, failure string, err error) {
	// Tester and Reviewer run in parallel — both panels show "running" at the same time.
	p.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RoleTester, bus.MsgEvent, "starting tester"))
	p.b.Publish(bus.NewMessage(bus.RoleSystem, bus.RoleReviewer, bus.MsgEvent, "starting reviewer"))

	var (
		wg           sync.WaitGroup
		testResp     bus.Message
		reviewResp   bus.Message
		testRunErr   error
		reviewRunErr error
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		testResp, testRunErr = p.runAgent(ctx, bus.RoleTester, agent.TesterPayload{})
	}()
	go func() {
		defer wg.Done()
		reviewResp, reviewRunErr = p.runAgent(ctx, bus.RoleReviewer, agent.ReviewerPayload{})
	}()
	wg.Wait()

	if testRunErr != nil {
		return false, false, "", fmt.Errorf("test: %w", testRunErr)
	}
	if reviewRunErr != nil {
		return false, false, "", fmt.Errorf("review: %w", reviewRunErr)
	}

	testResult, ok := testResp.Payload.(agent.TestReport)
	if !ok {
		return false, false, "", fmt.Errorf("tester returned unexpected payload type %T", testResp.Payload)
	}
	testOK = testResult.Success

	reviewResult, ok := reviewResp.Payload.(agent.ReviewResult)
	if !ok {
		return false, false, "", fmt.Errorf("reviewer returned unexpected payload type %T", reviewResp.Payload)
	}
	reviewOK = reviewResult.Approved && len(reviewResult.MustFix) == 0

	failure = buildFailure(testResult, reviewResult)
	return testOK, reviewOK, failure, nil
}

func (p *Pipeline) pr(ctx context.Context, reqs string) error {
	p.setState(PipelineDone)
	if _, err := p.runAgent(ctx, bus.RolePR, agent.PRPayload{Requirements: reqs}); err != nil {
		return fmt.Errorf("pr: %w", err)
	}
	p.event("run complete")
	return nil
}

func (p *Pipeline) runAgent(ctx context.Context, role bus.AgentRole, payload any) (bus.Message, error) {
	a, ok := p.agents[role]
	if !ok {
		return bus.Message{}, fmt.Errorf("no agent for role %q", role)
	}
	msg := bus.NewMessage(bus.RoleSystem, role, bus.MsgRequest, payload)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, role, bus.MsgEvent,
		fmt.Sprintf("starting %s", role)))

	resp, err := a.Run(ctx, msg)
	if err != nil {
		p.b.Publish(bus.NewMessage(role, "", bus.MsgEvent,
			fmt.Sprintf("error: %v", err)))
		return bus.Message{}, err
	}
	p.b.Publish(bus.NewMessage(role, "", bus.MsgEvent,
		fmt.Sprintf("%s complete", role)))
	return resp, nil
}

// waitApproval emits a human gate event and blocks until the approval file
// exists (CLI plain mode) or Approve() is called (TUI mode).
func (p *Pipeline) waitApproval(ctx context.Context, marker, hint string) error {
	p.setState(PipelineGate)
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgHumanGate, hint))

	for {
		if _, err := p.ws.ReadFile(marker); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.gateCh:
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

func (p *Pipeline) applyPatch() error {
	patchPath := p.ws.Path(artifacts.PatchFile)

	// Validate patch contents before applying: reject path traversal attempts.
	if err := validatePatch(patchPath); err != nil {
		return fmt.Errorf("patch validation: %w", err)
	}

	// Dry-run first: --check exits non-zero without modifying the worktree.
	check := exec.Command("git", "apply", "--check", patchPath)
	check.Dir = p.root
	var checkErr bytes.Buffer
	check.Stderr = &checkErr
	if err := check.Run(); err != nil {
		return fmt.Errorf("git apply --check: %w: %s", err, strings.TrimSpace(checkErr.String()))
	}

	cmd := exec.Command("git", "apply", patchPath)
	cmd.Dir = p.root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// validatePatch reads the patch file and rejects any path that contains ".."
// (which could escape the repository root).
func validatePatch(patchPath string) error {
	data, err := os.ReadFile(patchPath)
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			// Strip the "a/" or "b/" prefix git diff uses.
			path := strings.TrimPrefix(strings.TrimPrefix(line[4:], "a/"), "b/")
			if strings.Contains(path, "..") {
				return fmt.Errorf("line %d: path contains '..': %q", i+1, path)
			}
		}
	}
	return nil
}

func (p *Pipeline) setState(s PipelineState) {
	p.state = s
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, string(s)))
}

func (p *Pipeline) event(msg string) {
	p.b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, msg))
}

func buildFailure(test agent.TestReport, review agent.ReviewResult) string {
	var parts []string
	for _, c := range test.Commands {
		if c.ExitCode != 0 {
			parts = append(parts, fmt.Sprintf("$ %s\n%s\n%s", c.Command, c.Stdout, c.Stderr))
		}
	}
	parts = append(parts, review.MustFix...)
	return strings.Join(parts, "\n")
}
