package agent

import (
	"context"
	"fmt"

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
	Unparsed   bool
	RawOutput  string
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
	s := parseReviewSections(text, "RECOMMENDATION", "NICE TO HAVE")
	return SecurityResult{
		Approved:   s.Approved,
		MustFix:    s.MustFix,
		NiceToHave: s.NiceToHave,
		Unparsed:   !s.Parsed,
		RawOutput:  text,
	}
}
