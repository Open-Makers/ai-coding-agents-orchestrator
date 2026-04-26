package agent

import (
	"context"
	"fmt"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/tokenutil"
)

// QAPayload is the input for QA review.
type QAPayload struct {
	Files []string // source files to review
	Root  string   // project root for reading files
	Seeds []string // optional seed paths (graph + semantic) to expand context
}

// QAResult is the structured outcome of a QA review.
type QAResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
	Unparsed   bool
	RawOutput  string
}

// QAAgent audits code for edge cases, error handling, and overall quality assurance.
type QAAgent struct {
	BaseAgent
	runner           runner.LLMRunner
	ws               artifacts.Workspace
	skills           []string
	model            string
	maxContextTokens int
}

func NewQAAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *QAAgent {
	return &QAAgent{
		BaseAgent: NewBase(bus.RoleQA, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

// SetMaxContextTokens configures the token budget for this agent.
func (a *QAAgent) SetMaxContextTokens(n int) { a.maxContextTokens = n }

func (a *QAAgent) Role() bus.AgentRole { return bus.RoleQA }

func (a *QAAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, _ := msg.Payload.(QAPayload)
	plan, _ := a.ws.ReadFile(artifacts.ImplementationPlanFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	sourceContext := buildCompactSourceContext(payload.Root, payload.Files, a.maxContextTokens, payload.Seeds...)
	if sourceContext == "" {
		raw, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
		sourceContext = string(raw)
	}

	systemPrompt := prompts.MustLoad("qa-system")

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
		return bus.Message{}, fmt.Errorf("qa: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("qa: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.QAReviewFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	result := parseQAReview(output)
	return bus.NewMessage(bus.RoleQA, "", bus.MsgResponse, result), nil
}

func parseQAReview(text string) QAResult {
	s := parseReviewSections(text, "RECOMMENDATION", "NICE TO HAVE")
	return QAResult{
		Approved:   s.Approved,
		MustFix:    s.MustFix,
		NiceToHave: s.NiceToHave,
		Unparsed:   !s.Parsed,
		RawOutput:  text,
	}
}
