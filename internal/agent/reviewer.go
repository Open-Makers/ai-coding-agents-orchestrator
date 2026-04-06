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

type ReviewerPayload struct {
	PatchPath  string
	TestReport string
	PlanPath   string
}

// ReviewResult is the structured outcome of a review.
type ReviewResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
}

type ReviewerAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewReviewerAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *ReviewerAgent {
	return &ReviewerAgent{
		BaseAgent: NewBase(bus.RoleReviewer, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *ReviewerAgent) Role() bus.AgentRole { return bus.RoleReviewer }

func (a *ReviewerAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(ReviewerPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("reviewer: unexpected payload type %T", msg.Payload)
	}

	plan, _ := a.ws.ReadFile(artifacts.ImplementationPlanFile)
	coderOutput, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	_ = payload

	systemPrompt := prompts.MustLoad("reviewer-system")

	userContent := fmt.Sprintf("Plan:\n%s\n\nCode:\n%s\n\nTest results:\n%s",
		string(plan), string(coderOutput), string(report))

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("reviewer: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("reviewer: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.ReviewFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	result := parseReview(output)
	return bus.NewMessage(bus.RoleReviewer, "", bus.MsgResponse, result), nil
}

func parseReview(text string) ReviewResult {
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

	return ReviewResult{Approved: approved, MustFix: mustFix, NiceToHave: niceToHave}
}
