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

// UXReviewerPayload is the input for UX/UI review (reads artifacts from workspace).
type UXReviewerPayload struct{}

// UXReviewResult is the structured outcome of a UX/UI review.
type UXReviewResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
}

// UXReviewerAgent audits code for UX/UI quality, accessibility, and usability.
type UXReviewerAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewUXReviewerAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *UXReviewerAgent {
	return &UXReviewerAgent{
		BaseAgent: NewBase(bus.RoleUXReviewer, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *UXReviewerAgent) Role() bus.AgentRole { return bus.RoleUXReviewer }

func (a *UXReviewerAgent) Run(ctx context.Context, _ bus.Message) (bus.Message, error) {
	plan, _ := a.ws.ReadFile(artifacts.ImplementationPlanFile)
	coderOutput, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)

	systemPrompt := prompts.MustLoad("ux-reviewer-system")

	userContent := fmt.Sprintf("Plan:\n%s\n\nCode:\n%s", string(plan), string(coderOutput))

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("ux_reviewer: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("ux_reviewer: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.UXReviewFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	result := parseUXReview(output)
	return bus.NewMessage(bus.RoleUXReviewer, "", bus.MsgResponse, result), nil
}

func parseUXReview(text string) UXReviewResult {
	lines := strings.Split(text, "\n")
	var mustFix, niceToHave []string
	section := ""
	approved := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		upper := strings.ToUpper(trim)

		switch {
		case strings.HasPrefix(upper, "MUST FIX"):
			section = "mustfix"
		case strings.HasPrefix(upper, "NICE TO HAVE"):
			section = "nicetohave"
		case strings.HasPrefix(upper, "APPROVE"):
			approved = strings.Contains(upper, "YES")
			section = ""
		default:
			item := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			if item == "" || strings.ToLower(item) == "none" {
				continue
			}
			switch section {
			case "mustfix":
				mustFix = append(mustFix, item)
			case "nicetohave":
				niceToHave = append(niceToHave, item)
			}
		}
	}

	return UXReviewResult{Approved: approved, MustFix: mustFix, NiceToHave: niceToHave}
}
