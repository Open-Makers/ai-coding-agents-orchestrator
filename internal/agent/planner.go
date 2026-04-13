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

// PlannerPayload is the input message payload for PlannerAgent.
type PlannerPayload struct {
	Requirements   string
	MoscowPlan     string // MoSCoW prioritization from PM agent
	ProductVision  string // Product vision from PM agent
	ProjectContext string
}

// PlannerAgent generates architecture, implementation plan and prompts.
type PlannerAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
}

func NewPlannerAgent(b *bus.Bus, r runner.LLMRunner, ws artifacts.Workspace, skills []string, model string) *PlannerAgent {
	return &PlannerAgent{
		BaseAgent: NewBase(bus.RolePlanner, b),
		runner:    r,
		ws:        ws,
		skills:    skills,
		model:     model,
	}
}

func (a *PlannerAgent) Role() bus.AgentRole { return bus.RolePlanner }

func (a *PlannerAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(PlannerPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("planner: unexpected payload type %T", msg.Payload)
	}

	moscowContext := ""
	if payload.MoscowPlan != "" {
		moscowContext = fmt.Sprintf(`
The Product Manager has already created the MoSCoW prioritization below.
Use it as your guide — implement Must Have and Should Have features only.
Do NOT re-prioritize features. Follow the PM's priorities exactly.

Product Vision:
%s

MoSCoW Prioritization:
%s
`, payload.ProductVision, payload.MoscowPlan)
	}

	systemPrompt := fmt.Sprintf(prompts.MustLoad("planner-system"), moscowContext, payload.ProjectContext)

	userContent := fmt.Sprintf("Write the architecture, plan, and prompts for the following requirements. Remember: NO code, only descriptions.\n\nRequirements:\n%s", payload.Requirements)

	req := runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	}

	ch, err := a.runner.Complete(ctx, req)
	if err != nil {
		return bus.Message{}, fmt.Errorf("planner: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return bus.Message{}, fmt.Errorf("planner: stream: %w", err)
	}

	sections := parseSections(output, "ARCHITECTURE", "PLAN", "PROMPTS")
	arch := sections["ARCHITECTURE"]
	plan := sections["PLAN"]
	promptsContent := sections["PROMPTS"]

	// fallback: write everything if sections missing
	if arch == "" && plan == "" {
		arch = output
		plan = output
	}

	if err := a.ws.WriteFile(artifacts.ArchitectureFile, []byte(arch+"\n")); err != nil {
		return bus.Message{}, err
	}
	if err := a.ws.WriteFile(artifacts.ImplementationPlanFile, []byte(plan+"\n")); err != nil {
		return bus.Message{}, err
	}
	if promptsContent == "" {
		promptsContent = "(no prompts section generated — approve to continue)"
	}
	if err := a.ws.WriteFile(artifacts.PromptsFile, []byte(promptsContent+"\n")); err != nil {
		return bus.Message{}, err
	}

	return bus.NewMessage(bus.RolePlanner, "", bus.MsgResponse, artifacts.ImplementationPlanFile), nil
}

// Revise regenerates a single planning artifact given user feedback.
// The revised content is streamed to the bus (planner panel) and written back to the workspace.
func (a *PlannerAgent) Revise(ctx context.Context, artifactFile, feedback string) error {
	current, _ := a.ws.ReadFile(artifactFile)

	systemPrompt := prompts.MustLoad("planner-revise")
	userContent := fmt.Sprintf(
		"Current %s:\n\n%s\n\nFeedback:\n%s\n\nProvide the revised content:",
		artifactFile, string(current), feedback,
	)

	req := runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	}

	ch, err := a.runner.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("planner revise: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return fmt.Errorf("planner revise stream: %w", err)
	}

	return a.ws.WriteFile(artifactFile, []byte(output+"\n"))
}

// collectStream drains a token channel, emitting tokens to the bus and returning full text.
func (a *BaseAgent) collectStream(ch <-chan runner.Token) (string, error) {
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return sb.String(), tok.Error
		}
		if tok.Done {
			break
		}
		a.emitToken(tok.Text, false)
		sb.WriteString(tok.Text)
	}
	a.emitToken("", true)
	return sb.String(), nil
}

// parseSections extracts delimited sections from LLM output.
// Recognises multiple delimiter styles:
//   - ===KEY===
//   - ### KEY ===  /  ### KEY
//   - ## KEY
//   - **KEY**
func parseSections(output string, keys ...string) map[string]string {
	result := make(map[string]string, len(keys))
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[strings.ToUpper(k)] = true
	}

	current := ""
	for _, line := range strings.Split(output, "\n") {
		if name := extractSectionName(line, keySet); name != "" {
			current = name
			continue
		}
		if current != "" {
			result[current] += line + "\n"
		}
	}

	for k := range result {
		result[k] = strings.TrimSpace(result[k])
	}
	return result
}

// Stage represents a single implementation stage parsed from the PROMPTS section.
type Stage struct {
	Index  int
	Name   string
	Prompt string
}

// ParseStages splits the prompts section into ordered stages.
// It looks for ===STAGE N: description=== delimiters.
// If no stage delimiters are found, returns a single stage with the entire content.
func ParseStages(promptsContent string) []Stage {
	lines := strings.Split(promptsContent, "\n")
	var stages []Stage
	var currentLines []string
	currentIndex := 0
	currentName := ""

	flushStage := func() {
		if currentIndex > 0 {
			stages = append(stages, Stage{
				Index:  currentIndex,
				Name:   currentName,
				Prompt: strings.TrimSpace(strings.Join(currentLines, "\n")),
			})
		}
	}

	for _, line := range lines {
		idx, name := parseStageHeader(line)
		if idx > 0 {
			flushStage()
			currentIndex = idx
			currentName = name
			currentLines = nil
			continue
		}
		if currentIndex > 0 {
			currentLines = append(currentLines, line)
		}
	}
	flushStage()

	if len(stages) == 0 {
		return []Stage{{
			Index:  1,
			Name:   "Full Implementation",
			Prompt: strings.TrimSpace(promptsContent),
		}}
	}
	return stages
}

// parseStageHeader checks if a line is a stage delimiter.
// Recognised patterns:
//   - ===STAGE 1: description===
//   - ### Stage 1: description
//   - ### Step 1: description
//   - ### 1. description  (numbered markdown heading)
//
// Returns (index, name) or (0, "") if not a stage header.
func parseStageHeader(line string) (int, string) {
	trimmed := strings.TrimSpace(line)

	// Strip === and other decorators.
	cleaned := strings.Trim(trimmed, "=#* \t")
	cleanedUpper := strings.ToUpper(cleaned)

	// Try "STAGE N" or "STEP N" prefix.
	var rest string
	switch {
	case strings.HasPrefix(cleanedUpper, "STAGE "):
		rest = cleaned[len("STAGE "):]
	case strings.HasPrefix(cleanedUpper, "STEP "):
		rest = cleaned[len("STEP "):]
	default:
		// Try numbered heading: starts with digit (e.g. "1. description" or "1: description").
		if len(cleaned) > 0 && cleaned[0] >= '0' && cleaned[0] <= '9' {
			rest = cleaned
		} else {
			return 0, ""
		}
	}

	return parseIndexAndName(rest)
}

// parseIndexAndName extracts a number and trailing description from strings like
// "1: description", "2 - description", "3. description".
func parseIndexAndName(rest string) (int, string) {
	var indexStr string
	var name string
	for i, c := range rest {
		if c >= '0' && c <= '9' {
			indexStr += string(c)
			continue
		}
		if c == ':' || c == '-' || c == '–' || c == '—' || c == '.' {
			name = strings.TrimSpace(rest[i+1:])
			break
		}
		if c == ' ' && indexStr != "" {
			name = strings.TrimSpace(rest[i+1:])
			name = strings.TrimLeft(name, ":-–—. ")
			break
		}
		break
	}

	if indexStr == "" {
		return 0, ""
	}

	idx := 0
	for _, c := range indexStr {
		idx = idx*10 + int(c-'0')
	}
	return idx, name
}

// extractSectionName tries to extract a known section key from a delimiter line.
// Tolerates LLM formatting variations: extra text after the key ("PROMPTS (Stage-by-Stage)"),
// markdown decorators (===, ###, **, etc.), and mixed-case headers.
func extractSectionName(line string, keySet map[string]bool) string {
	trim := strings.TrimSpace(line)
	if trim == "" {
		return ""
	}

	// Must look like a header — require some kind of decorator or standalone keyword.
	hasDecorator := strings.HasPrefix(trim, "=") || strings.HasPrefix(trim, "#") ||
		strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "_")

	// Strip common decorators: ===, ###, ##, **, *
	cleaned := strings.Trim(trim, "=#*_ \t")
	cleaned = strings.TrimSpace(cleaned)
	candidate := strings.ToUpper(cleaned)

	// Exact match.
	if keySet[candidate] {
		return candidate
	}

	// Only try prefix matching on decorator lines to avoid false positives on prose.
	if !hasDecorator {
		return ""
	}

	// Prefix match: "PROMPTS (STAGE-BY-STAGE)" → PROMPTS, "MOSCOW PRIORITIZATION" → MOSCOW.
	for key := range keySet {
		if strings.HasPrefix(candidate, key) {
			tail := candidate[len(key):]
			if tail == "" || tail[0] == ' ' || tail[0] == '(' || tail[0] == '-' || tail[0] == ':' {
				return key
			}
		}
	}
	return ""
}
