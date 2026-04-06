package agent

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// PMPayload is the input for the Project Manager agent.
type PMPayload struct {
	Requirements   string
	ProjectContext string
}

// PMAgent creates product vision and MoSCoW-prioritized feature list.
type PMAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewPMAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *PMAgent {
	return &PMAgent{
		BaseAgent: NewBase(bus.RolePM, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *PMAgent) Role() bus.AgentRole { return bus.RolePM }

func (a *PMAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(PMPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("pm: unexpected payload type %T", msg.Payload)
	}

	systemPrompt := fmt.Sprintf(prompts.MustLoad("pm-system"), payload.ProjectContext)

	userContent := fmt.Sprintf("Create the product vision and MoSCoW feature prioritization for the following requirements.\n\nRequirements:\n%s", payload.Requirements)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("pm: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("pm: stream: %w", err)
	}

	sections := parseSections(output, "VISION", "MOSCOW")
	vision := sections["VISION"]
	moscow := sections["MOSCOW"]

	if vision == "" && moscow == "" {
		vision = output
		moscow = output
	}

	if err := a.ws.WriteFile(artifacts.VisionFile, []byte(vision+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.MoscowFile, []byte(moscow+"\n")); err != nil {
		return bus.Message{}, err
	}

	return bus.NewMessage(bus.RolePM, "", bus.MsgResponse, artifacts.MoscowFile), nil
}
