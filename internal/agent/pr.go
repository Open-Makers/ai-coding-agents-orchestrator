package agent

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

type PRPayload struct {
	Requirements string
}

type PRAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewPRAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *PRAgent {
	return &PRAgent{
		BaseAgent: NewBase(bus.RolePR, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *PRAgent) Role() bus.AgentRole { return bus.RolePR }

func (a *PRAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(PRPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("pr: unexpected payload type %T", msg.Payload)
	}

	changes, _ := a.ws.ReadFile(artifacts.ChangesFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)
	review, _ := a.ws.ReadFile(artifacts.ReviewFile)

	systemPrompt := `You are a Release Captain. Produce a clear PR description in Markdown.
Include: summary, what changed, how to test.`

	userContent := fmt.Sprintf("Goal:\n%s\n\nChanges:\n%s\n\nTests:\n%s\n\nReview:\n%s",
		payload.Requirements, string(changes), string(report), string(review))

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("pr: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("pr: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.PRDescriptionFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	return bus.NewMessage(bus.RolePR, "", bus.MsgResponse, artifacts.PRDescriptionFile), nil
}
