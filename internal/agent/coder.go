package agent

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

type CoderPayload struct {
	Plan           string
	ProjectContext string
	Scope          []string
}

type CoderAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewCoderAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *CoderAgent {
	return &CoderAgent{
		BaseAgent: NewBase(bus.RoleCoder, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *CoderAgent) Role() bus.AgentRole { return bus.RoleCoder }

func (a *CoderAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(CoderPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("coder: unexpected payload type %T", msg.Payload)
	}

	systemPrompt := fmt.Sprintf(`You are a Senior Engineer. Implement only what is described in the plan.
Prefer small, readable changes.

OUTPUT FORMAT (required — do not skip any section):
===PATCH===
<unified diff suitable for git apply>
===CHANGES===
<what changed and why, markdown>
===TEST_CMDS===
<shell commands to verify the changes, one per line>

%s`, payload.ProjectContext)

	userContent := fmt.Sprintf("Plan:\n%s", payload.Plan)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("coder: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("coder: stream: %w", err)
	}

	sections := parseSections(output, "PATCH", "CHANGES", "TEST_CMDS")

	if err := a.ws.WriteFile(artifacts.PatchFile, []byte(sections["PATCH"]+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.ChangesFile, []byte(sections["CHANGES"]+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.TestCmdsFile, []byte(sections["TEST_CMDS"]+"\n")); err != nil {
		return bus.Message{}, err
	}

	a.Bus.Publish(bus.NewMessage(bus.RoleCoder, "", bus.MsgEvent, "EventPatchReady"))

	return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, artifacts.PatchFile), nil
}
