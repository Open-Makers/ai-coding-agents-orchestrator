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

// QAPayload is the input for QA review (reads artifacts from workspace).
type QAPayload struct{}

// QAResult is the structured outcome of a QA review.
type QAResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
}

// QAAgent audits code for edge cases, error handling, and overall quality assurance.
type QAAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
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

func (a *QAAgent) Role() bus.AgentRole { return bus.RoleQA }

func (a *QAAgent) Run(ctx context.Context, _ bus.Message) (bus.Message, error) {
	plan, _ := a.ws.ReadFile(artifacts.ImplementationPlanFile)
	coderOutput, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	systemPrompt := prompts.MustLoad("qa-system")

	userContent := fmt.Sprintf("Plan:\n%s\n\nCode:\n%s\n\nTest results:\n%s",
		string(plan), string(coderOutput), string(report))

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
		case strings.HasPrefix(upper, "RECOMMENDATION"):
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

	return QAResult{Approved: approved, MustFix: mustFix, NiceToHave: niceToHave}
}
