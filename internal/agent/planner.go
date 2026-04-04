package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// PlannerPayload is the input message payload for PlannerAgent.
type PlannerPayload struct {
	Requirements   string
	ProjectContext string
}

// PlannerAgent generates architecture, implementation plan and prompts.
type PlannerAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewPlannerAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *PlannerAgent {
	return &PlannerAgent{
		BaseAgent: NewBase(bus.RolePlanner, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *PlannerAgent) Role() bus.AgentRole { return bus.RolePlanner }

func (a *PlannerAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(PlannerPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("planner: unexpected payload type %T", msg.Payload)
	}

	systemPrompt := fmt.Sprintf(`You are a Tech Lead. Break the task into steps, identify files, risks, and a test plan.
Produce three outputs in order, each wrapped in markers:

===ARCHITECTURE===
<proposed architecture>
===PLAN===
<implementation steps, files to change, risks>
===PROMPTS===
<prompts for each implementation phase>

%s`, payload.ProjectContext)

	userContent := fmt.Sprintf("Requirements:\n%s", payload.Requirements)

	req := runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	}

	ch, err := a.runner.Complete(ctx, req)
	if err != nil {
		return bus.Message{}, fmt.Errorf("planner: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("planner: stream: %w", err)
	}

	sections := parseSections(output, "ARCHITECTURE", "PLAN", "PROMPTS")
	arch := sections["ARCHITECTURE"]
	plan := sections["PLAN"]
	prompts := sections["PROMPTS"]

	// fallback: write everything if sections missing
	if arch == "" && plan == "" {
		arch = output
		plan = output
	}

	if err := a.ws.WriteFile(artifacts.ArchitectureFile, []byte(arch+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.ImplementationPlanFile, []byte(plan+"\n")); err != nil {
		return bus.Message{}, err
	}
	if prompts != "" {
		if err := a.ws.WriteFile(artifacts.PromptsFile, []byte(prompts+"\n")); err != nil {
			return bus.Message{}, err
		}
	}

	return bus.NewMessage(bus.RolePlanner, "", bus.MsgResponse, artifacts.ImplementationPlanFile), nil
}

// collectStream drains a token channel, emitting tokens to the bus and returning full text.
func (a *BaseAgent) collectStream(ch <-chan runner.Token) (string, error) {
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return sb.String(), tok.Error
		}
		if tok.Done {
			break
		}
		a.emitToken(tok.Text, false)
		sb.WriteString(tok.Text)
	}
	a.emitToken("", true)
	return sb.String(), nil
}

// parseSections extracts ===KEY=== delimited sections from output.
func parseSections(output string, keys ...string) map[string]string {
	result := make(map[string]string, len(keys))
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}

	current := ""
	for _, line := range strings.Split(output, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "===") && strings.HasSuffix(trim, "===") {
			name := strings.TrimSpace(strings.Trim(trim, "="))
			if keySet[name] {
				current = name
				continue
			}
		}
		if current != "" {
			result[current] += line + "\n"
		}
	}

	for k := range result {
		result[k] = strings.TrimSpace(result[k])
	}
	return result
}
