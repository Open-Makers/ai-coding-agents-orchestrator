package agent

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// ArchitectPayload is the input message payload for ArchitectAgent.
type ArchitectPayload struct {
	Requirements   string
	MoscowPlan     string // MoSCoW prioritization from PM agent
	ProductVision  string // Product vision from PM agent
	ProjectContext string
}

// ArchitectAgent produces a concise architecture document for human approval.
// It runs after the PM step and before the Planner, so the architecture is
// agreed on a separate gate before the Tech Lead derives the implementation plan.
type ArchitectAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewArchitectAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *ArchitectAgent {
	return &ArchitectAgent{
		BaseAgent: NewBase(bus.RoleArchitect, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *ArchitectAgent) Role() bus.AgentRole { return bus.RoleArchitect }

func (a *ArchitectAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(ArchitectPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("architect: unexpected payload type %T", msg.Payload)
	}

	moscowContext := ""
	if payload.MoscowPlan != "" {
		moscowContext = fmt.Sprintf(`
The Product Manager has already created the MoSCoW prioritization below.
Use it as your guide — design for Must Have and Should Have features only.

Product Vision:
%s

MoSCoW Prioritization:
%s
`, payload.ProductVision, payload.MoscowPlan)
	}

	systemPrompt := fmt.Sprintf(prompts.MustLoad("architect-system"), moscowContext, payload.ProjectContext)
	userContent := fmt.Sprintf("Produce the architecture document for the following requirements. Be concise.\n\nRequirements:\n%s", payload.Requirements)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("architect: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("architect: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.ArchitectureFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	return bus.NewMessage(bus.RoleArchitect, "", bus.MsgResponse, artifacts.ArchitectureFile), nil
}

// Revise regenerates the architecture document given user feedback.
func (a *ArchitectAgent) Revise(ctx context.Context, artifactFile, feedback string) error {
	current, _ := a.ws.ReadFile(artifactFile)

	systemPrompt := prompts.MustLoad("planner-revise")
	userContent := fmt.Sprintf(
		"Current %s:\n\n%s\n\nFeedback:\n%s\n\nProvide the revised content:",
		artifactFile, string(current), feedback,
	)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return fmt.Errorf("architect revise: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return fmt.Errorf("architect revise stream: %w", err)
	}

	return a.ws.WriteFile(artifactFile, []byte(output+"\n"))
}
