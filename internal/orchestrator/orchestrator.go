package orchestrator

import (
	"fmt"
	"os"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
)

type Orchestrator struct {
	Config       Config
	Runner       Runner
	Tester       Tester
	ReviewParser ReviewParser
	Patcher      Patcher
}

func (o Orchestrator) Run(root, taskPath string) error {
	if o.Runner == nil {
		return fmt.Errorf("runner is required")
	}
	if o.Tester == nil {
		return fmt.Errorf("tester is required")
	}
	if o.ReviewParser == nil {
		return fmt.Errorf("review parser is required")
	}
	if o.Patcher == nil {
		return fmt.Errorf("patcher is required")
	}

	maxFix := o.Config.MaxFixAttempts
	if maxFix <= 0 {
		maxFix = 3
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		return err
	}

	ctx := &Context{
		Root:      root,
		TaskPath:  taskPath,
		Workspace: ws,
		State:     StateArchitecture,
	}

	appendRunLog(ctx, "run started")

	if err := o.Runner.Architecture(ctx); err != nil {
		appendRunLog(ctx, "architecture failed")
		return err
	}
	appendRunLog(ctx, "architecture complete")
	if err := requireApproval(ctx, artifacts.ArchitectureApprovedFile); err != nil {
		return err
	}

	ctx.State = StatePlan
	if err := o.Runner.Plan(ctx); err != nil {
		appendRunLog(ctx, "plan failed")
		return err
	}
	appendRunLog(ctx, "plan complete")
	if err := requireApproval(ctx, artifacts.PlanApprovedFile); err != nil {
		return err
	}

	ctx.State = StatePrompts
	if err := o.Runner.Prompts(ctx); err != nil {
		appendRunLog(ctx, "prompts failed")
		return err
	}
	appendRunLog(ctx, "prompts complete")
	if err := requireApproval(ctx, artifacts.PromptsApprovedFile); err != nil {
		return err
	}

	ctx.State = StateCode
	appendRunLog(ctx, "code started")
	if err := o.Runner.Code(ctx); err != nil {
		appendRunLog(ctx, "code failed")
		return err
	}
	if err := o.Patcher.Apply(ctx, ctx.Workspace.Path(artifacts.PatchFile)); err != nil {
		appendRunLog(ctx, "patch apply failed")
		return err
	}
	appendRunLog(ctx, "code complete")

	ctx.State = StateTest
	appendRunLog(ctx, "tests started")
	result, err := o.Tester.Run(ctx)
	if err != nil {
		appendRunLog(ctx, "tests failed (runner error)")
		return err
	}
	if !result.Success {
		appendRunLog(ctx, "tests failed")
		return o.runFixLoop(ctx, FailureContext{Test: &result}, maxFix)
	}
	appendRunLog(ctx, "tests passed")

	ctx.State = StateReview
	appendRunLog(ctx, "review started")
	if err := o.Runner.Review(ctx); err != nil {
		appendRunLog(ctx, "review failed")
		return err
	}
	review, err := o.ReviewParser.Parse(ctx)
	if err != nil {
		appendRunLog(ctx, "review parse failed")
		return err
	}
	if !review.Approve || len(review.MustFix) > 0 {
		appendRunLog(ctx, "review not approved")
		return o.runFixLoop(ctx, FailureContext{Review: &review}, maxFix)
	}
	appendRunLog(ctx, "review approved")

	ctx.State = StateDone
	appendRunLog(ctx, "done started")
	if err := o.Runner.Docs(ctx); err != nil {
		appendRunLog(ctx, "done failed")
		return err
	}
	appendRunLog(ctx, "run complete")

	return nil
}

func (o Orchestrator) runFixLoop(ctx *Context, failure FailureContext, maxFix int) error {
	for attempts := 0; attempts < maxFix; attempts++ {
		ctx.State = StateFix
		ctx.FixAttempts = attempts + 1
		appendRunLog(ctx, fmt.Sprintf("fix attempt %d started", ctx.FixAttempts))
		if err := o.Runner.Fix(ctx, failure); err != nil {
			appendRunLog(ctx, "fix failed")
			return err
		}
		if err := o.Patcher.Apply(ctx, ctx.Workspace.Path(artifacts.PatchFile)); err != nil {
			appendRunLog(ctx, "patch apply failed")
			return err
		}
		appendRunLog(ctx, fmt.Sprintf("fix attempt %d applied", ctx.FixAttempts))

		ctx.State = StateTest
		appendRunLog(ctx, "tests started")
		result, err := o.Tester.Run(ctx)
		if err != nil {
			appendRunLog(ctx, "tests failed (runner error)")
			return err
		}
		if !result.Success {
			appendRunLog(ctx, "tests failed")
			failure = FailureContext{Test: &result}
			continue
		}
		appendRunLog(ctx, "tests passed")

		ctx.State = StateReview
		appendRunLog(ctx, "review started")
		if err := o.Runner.Review(ctx); err != nil {
			appendRunLog(ctx, "review failed")
			return err
		}
		review, err := o.ReviewParser.Parse(ctx)
		if err != nil {
			appendRunLog(ctx, "review parse failed")
			return err
		}
		if !review.Approve || len(review.MustFix) > 0 {
			appendRunLog(ctx, "review not approved")
			failure = FailureContext{Review: &review}
			continue
		}
		appendRunLog(ctx, "review approved")

		ctx.State = StateDone
		appendRunLog(ctx, "done started")
		return o.Runner.Docs(ctx)
	}
	appendRunLog(ctx, "run failed: exceeded max fix attempts")
	return fmt.Errorf("exceeded max fix attempts (%d)", maxFix)
}

func requireApproval(ctx *Context, marker string) error {
	waitLogged := false
	for {
		if _, err := ctx.Workspace.ReadFile(marker); err == nil {
			appendRunLog(ctx, fmt.Sprintf("approval found: %s", marker))
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}

		if !waitLogged {
			appendRunLog(ctx, fmt.Sprintf("waiting for approval: %s", marker))
			waitLogged = true
		}
		time.Sleep(5 * time.Second)
	}
}

func appendRunLog(ctx *Context, message string) {
	_ = ctx.Workspace.AppendRunLog(message)
}
