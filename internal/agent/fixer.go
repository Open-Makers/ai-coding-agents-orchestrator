package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

type FixerPayload struct {
	Failure string // error logs or review must-fix items
}

type FixerAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewFixerAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *FixerAgent {
	return &FixerAgent{
		BaseAgent: NewBase(bus.RoleFixer, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *FixerAgent) Role() bus.AgentRole { return bus.RoleFixer }

func (a *FixerAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(FixerPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("fixer: unexpected payload type %T", msg.Payload)
	}

	patch, _ := a.ws.ReadFile(artifacts.PatchFile)

	systemPrompt := `You are a Debugger. Fix failures using the smallest possible patch.

OUTPUT FORMAT (required):
===PATCH===
<unified diff suitable for git apply>
===VERIFY===
<shell commands to verify the fix, one per line>`

	userContent := fmt.Sprintf("Error:\n%s\n\nCurrent diff:\n%s\n\nGoal: Fix and provide a patch plus verification steps.",
		payload.Failure, string(patch))

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("fixer: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("fixer: stream: %w", err)
	}

	sections := parseSections(output, "PATCH", "VERIFY")

	newPatch := sections["PATCH"]
	if newPatch == "" {
		newPatch = output
	}

	if err := a.ws.WriteFile(artifacts.PatchFile, []byte(newPatch+"\n")); err != nil {
		return bus.Message{}, err
	}

	verifyCmds := sections["VERIFY"]
	if verifyCmds != "" {
		existing, _ := a.ws.ReadFile(artifacts.TestCmdsFile)
		merged := strings.TrimSpace(string(existing)) + "\n" + verifyCmds
		if err := a.ws.WriteFile(artifacts.TestCmdsFile, []byte(strings.TrimSpace(merged)+"\n")); err != nil {
			a.emit(bus.MsgEvent, fmt.Sprintf("warning: could not update test_cmds: %v", err))
		}
	}

	return bus.NewMessage(bus.RoleFixer, "", bus.MsgResponse, artifacts.PatchFile), nil
}
