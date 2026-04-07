package agent

import (
	"context"
	"fmt"
	"strings"

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

// ArbitrateResult is the PM's decision on an unparseable review response.
type ArbitrateResult struct {
	MustFix []string
	Pass    bool
}

// Arbitrate asks the PM to interpret a raw, unparseable review output and
// decide whether the coder needs to fix something. This is the "default"
// fallback when review parsers cannot extract structured data from the LLM.
func (a *PMAgent) Arbitrate(ctx context.Context, phase, rawReviewOutput string) (ArbitrateResult, error) {
	systemPrompt := prompts.MustLoad("pm-arbitrate")

	userContent := fmt.Sprintf("Review phase: %s\n\nRaw reviewer output:\n%s", phase, rawReviewOutput)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return ArbitrateResult{}, fmt.Errorf("pm arbitrate: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return ArbitrateResult{}, fmt.Errorf("pm arbitrate: stream: %w", err)
	}

	return parseArbitrateResult(output), nil
}

// parseArbitrateResult parses the PM's arbitration response.
func parseArbitrateResult(text string) ArbitrateResult {
	lines := strings.Split(text, "\n")
	var mustFix []string
	pass := true
	section := ""

	for _, line := range lines {
		norm := normalizeHeading(line)
		upper := strings.ToUpper(norm)

		switch {
		case strings.Contains(upper, "VERDICT") && strings.Contains(upper, "FIX"):
			pass = false
		case strings.Contains(upper, "VERDICT") && strings.Contains(upper, "PASS"):
			pass = true
		case headingContains(norm, "MUST FIX", "MUST-FIX"):
			section = "mustfix"
		default:
			if section == "mustfix" {
				item := extractListItem(line)
				if item != "" && !isNoneValue(item) {
					mustFix = append(mustFix, item)
				}
			}
		}
	}

	if len(mustFix) > 0 {
		pass = false
	}

	return ArbitrateResult{MustFix: mustFix, Pass: pass}
}
