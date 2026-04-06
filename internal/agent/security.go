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

// SecurityPayload is the input for security review (reads artifacts from workspace).
type SecurityPayload struct{}

// SecurityResult is the structured outcome of a security review.
type SecurityResult struct {
	Approved   bool
	MustFix    []string
	NiceToHave []string
}

// SecurityAgent performs a dedicated security audit of the generated code.
type SecurityAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewSecurityAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *SecurityAgent {
	return &SecurityAgent{
		BaseAgent: NewBase(bus.RoleSecurity, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *SecurityAgent) Role() bus.AgentRole { return bus.RoleSecurity }

func (a *SecurityAgent) Run(ctx context.Context, _ bus.Message) (bus.Message, error) {
	coderOutput, _ := a.ws.ReadFile(artifacts.RawCoderOutputFile)
	report, _ := a.ws.ReadFile(artifacts.TestReportFile)

	systemPrompt := prompts.MustLoad("security-system")

	userContent := fmt.Sprintf("Code:\n%s\n\nTest results:\n%s",
		string(coderOutput), string(report))

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return bus.Message{}, fmt.Errorf("security: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("security: stream: %w", err)
	}

	if err := a.ws.WriteFile(artifacts.SecurityReviewFile, []byte(output+"\n")); err != nil {
		return bus.Message{}, err
	}

	result := parseSecurityReview(output)
	return bus.NewMessage(bus.RoleSecurity, "", bus.MsgResponse, result), nil
}

func parseSecurityReview(text string) SecurityResult {
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

	return SecurityResult{Approved: approved, MustFix: mustFix, NiceToHave: niceToHave}
}
