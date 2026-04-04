package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

type ReviewerPayload struct {
	PatchPath  string
	TestReport string
	PlanPath   string
}

// ReviewResult is the structured outcome of a review.
type ReviewResult struct {
	Approved bool
	MustFix  []string
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
	patch, _ := a.ws.ReadFile(artifacts.PatchFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	_ = payload

	systemPrompt := `You are a Code Reviewer. Look for logic errors, edge cases, security issues, and plan compliance.

Respond in this exact format:
MUST FIX
- <issue> (or "none" if no issues)
NICE TO HAVE
- <suggestion> (or "none")
Approve?: YES or NO`

	userContent := fmt.Sprintf("Plan:\n%s\n\nDiff:\n%s\n\nTest results:\n%s",
		string(plan), string(patch), string(report))

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
	var mustFix []string
	inMustFix := false
	approved := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		upper := strings.ToUpper(trim)

		switch {
		case strings.HasPrefix(upper, "MUST FIX"):
			inMustFix = true
		case strings.HasPrefix(upper, "NICE TO HAVE"):
			inMustFix = false
		case strings.HasPrefix(upper, "APPROVE"):
			approved = strings.Contains(upper, "YES")
			inMustFix = false
		default:
			if inMustFix {
				item := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
				if item != "" && strings.ToLower(item) != "none" {
					mustFix = append(mustFix, item)
				}
			}
		}
	}

	return ReviewResult{Approved: approved, MustFix: mustFix}
}
