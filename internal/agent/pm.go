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

// minArtifactLength is the minimum acceptable content length for PM artifacts.
// Artifacts shorter than this are considered generation failures.
const minArtifactLength = 20

// PMPayload is the input for the Project Manager agent.
type PMPayload struct {
	Requirements   string
	ProjectContext string
}

// PMAgent creates product vision and MoSCoW-prioritized feature list.
type PMAgent struct {
	BaseAgent
	runner      runner.LLMRunner
	fixerRunner runner.LLMRunner // optional: separate runner for retries with a better model
	ws          artifacts.Workspace
	skills      []string
	model       string
	fixerModel  string
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

// SetFixerRunner configures an optional separate runner/model used for retrying
// artifact generation when the primary model produces empty or insufficient output.
func (a *PMAgent) SetFixerRunner(r runner.LLMRunner, model string) {
	a.fixerRunner = r
	a.fixerModel = model
}

func (a *PMAgent) Role() bus.AgentRole { return bus.RolePM }

func (a *PMAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(PMPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("pm: unexpected payload type %T", msg.Payload)
	}

	vision, moscow, err := a.generate(ctx, payload, a.runner, a.model)
	if err != nil {
		return bus.Message{}, fmt.Errorf("pm: %w", err)
	}

	if a.fixerRunner != nil && (len(strings.TrimSpace(vision)) < minArtifactLength || len(strings.TrimSpace(moscow)) < minArtifactLength) {
		a.emitEvent("artifact too short, retrying with fixer model…")
		retryVision, retryMoscow, retryErr := a.generate(ctx, payload, a.fixerRunner, a.fixerModel)
		if retryErr == nil {
			if len(strings.TrimSpace(retryVision)) > len(strings.TrimSpace(vision)) {
				vision = retryVision
			}
			if len(strings.TrimSpace(retryMoscow)) > len(strings.TrimSpace(moscow)) {
				moscow = retryMoscow
			}
		}
	}

	if err := a.ws.WriteFile(artifacts.VisionFile, []byte(vision+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.MoscowFile, []byte(moscow+"\n")); err != nil {
		return bus.Message{}, err
	}

	return bus.NewMessage(bus.RolePM, "", bus.MsgResponse, artifacts.MoscowFile), nil
}

// generate runs the LLM completion and extracts vision/moscow sections.
func (a *PMAgent) generate(ctx context.Context, payload PMPayload, r runner.LLMRunner, model string) (string, string, error) {
	systemPrompt := fmt.Sprintf(prompts.MustLoad("pm-system"), payload.ProjectContext)

	userContent := fmt.Sprintf("Create the product vision and MoSCoW feature prioritization for the following requirements.\n\nRequirements:\n%s", payload.Requirements)

	ch, err := r.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return "", "", fmt.Errorf("runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return "", "", fmt.Errorf("stream: %w", err)
	}

	sections := parseSections(output, "VISION", "MOSCOW")
	vision := sections["VISION"]
	moscow := sections["MOSCOW"]

	if vision == "" && moscow == "" {
		vision = output
		moscow = output
	} else if vision == "" {
		vision = output
	} else if moscow == "" {
		moscow = output
	}

	return vision, moscow, nil
}

// emitEvent publishes a system event about the PM agent status.
func (a *PMAgent) emitEvent(text string) {
	a.Bus.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgEvent,
		bus.TokenPayload{Text: text + "\n", Done: false}))
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
