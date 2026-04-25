package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

type ReviewerPayload struct {
	Files          []string // source files to review (read from disk, not raw output)
	Root           string   // project root for reading files
	ProjectContext string   // optional repository briefing (brownfield, tree, etc.)
}

// ReviewResult is the structured outcome of a review.
type ReviewResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
	// Unparsed is true when the LLM response did not match any expected format.
	// The orchestrator should escalate this to PM for arbitration.
	Unparsed bool
	// RawOutput holds the original LLM text for PM arbitration when Unparsed is true.
	RawOutput string
}

type ReviewerAgent struct {
	BaseAgent
	runner           runner.LLMRunner
	ws               artifacts.Workspace
	skills           []string
	model            string
	maxContextTokens int
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

// SetMaxContextTokens configures the token budget for this agent.
func (a *ReviewerAgent) SetMaxContextTokens(n int) { a.maxContextTokens = n }

func (a *ReviewerAgent) Role() bus.AgentRole { return bus.RoleReviewer }

func (a *ReviewerAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(ReviewerPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("reviewer: unexpected payload type %T", msg.Payload)
	}

	plan, _ := a.ws.ReadFile(artifacts.ImplementationPlanFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	// Build source context from actual files on disk instead of raw coder output.
	// This avoids sending markdown decoration, LLM commentary, and duplicate content.
	sourceContext := buildCompactSourceContext(payload.Root, payload.Files, a.maxContextTokens)
	if sourceContext == "" {
		// Fallback to raw output if no files available.
		raw, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
		sourceContext = string(raw)
	}

	systemPrompt := prompts.MustLoad("reviewer-system")
	if strings.TrimSpace(payload.ProjectContext) != "" {
		systemPrompt = fmt.Sprintf("%s\n\nProject context:\n%s", systemPrompt, payload.ProjectContext)
	}

	userContent := fmt.Sprintf("Plan:\n%s\n\nCode:\n%s\n\nTest results:\n%s",
		string(plan), sourceContext, string(report))

	if a.maxContextTokens > 0 {
		userContent = tokenutil.Truncate(userContent, a.maxContextTokens)
	}

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
	s := parseReviewSections(text, "NICE TO HAVE", "RECOMMENDATION")
	return ReviewResult{
		Approved:   s.Approved,
		MustFix:    s.MustFix,
		NiceToHave: s.NiceToHave,
		Unparsed:   !s.Parsed,
		RawOutput:  text,
	}
}
