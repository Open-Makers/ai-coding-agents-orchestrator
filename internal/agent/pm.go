package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// PMPayload is the input for the Project Manager agent.
type PMPayload struct {
	Requirements   string
	ProjectContext string
}

// PMAgent creates product vision and MoSCoW-prioritized feature list.
type PMAgent struct {
	BaseAgent
	runner runner.LLMRunner
	ws     artifacts.Workspace
	skills []string
	model  string
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

func (a *PMAgent) Role() bus.AgentRole { return bus.RolePM }

// GatherRequirements conducts a multi-turn conversation with the user to produce
// project requirements. The flow is:
//  1. PM asks clarifying questions until it has enough information
//  2. PM produces a ===SUMMARY=== — caller publishes it and waits for approval
//  3. User confirms summary (via humanCh)
//  4. PM produces ===REQUIREMENTS=== — returned to caller
//
// The caller (Pipeline/TaskRunner) is responsible for the approval gates.
func (a *PMAgent) GatherRequirements(ctx context.Context, projectCtx string, humanCh <-chan string) (summary string, requirements string, err error) {
	systemPrompt := fmt.Sprintf(prompts.MustLoad("pm-gather"), projectCtx)

	messages := []runner.ConvMessage{}

	select {
	case reply, ok := <-humanCh:
		if !ok {
			return "", "", fmt.Errorf("pm gather: human channel closed")
		}
		messages = append(messages, runner.ConvMessage{Role: "user", Content: reply})
	case <-ctx.Done():
		return "", "", ctx.Err()
	}

	// Phase 1: Conversation → produce SUMMARY.
	for {
		output, streamErr := a.completeGatherTurn(ctx, systemPrompt, messages)
		if streamErr != nil {
			return "", "", fmt.Errorf("pm gather: %w", streamErr)
		}

		// Check if PM produced a summary.
		if s := parseSummarySection(output); s != "" {
			summary = s
			messages = append(messages, runner.ConvMessage{Role: "assistant", Content: output})
			break
		}

		// PM is asking questions — relay to human.
		a.Bus.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgConversation,
			bus.ConversationPayload{From: "pm", Content: output}))

		select {
		case reply, ok := <-humanCh:
			if !ok {
				return "", "", fmt.Errorf("pm gather: human channel closed")
			}
			messages = append(messages,
				runner.ConvMessage{Role: "assistant", Content: output},
				runner.ConvMessage{Role: "user", Content: reply},
			)
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}

	// Publish summary for display, then wait for user confirmation.
	a.Bus.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgConversation,
		bus.ConversationPayload{From: "pm", Content: summary}))

	select {
	case reply, ok := <-humanCh:
		if !ok {
			return "", "", fmt.Errorf("pm gather: human channel closed after summary")
		}
		messages = append(messages, runner.ConvMessage{Role: "user", Content: reply})
	case <-ctx.Done():
		return "", "", ctx.Err()
	}

	// Phase 2: Generate REQUIREMENTS based on the approved summary.
	output, streamErr := a.completeGatherTurn(ctx, systemPrompt, messages)
	if streamErr != nil {
		return "", "", fmt.Errorf("pm gather requirements: %w", streamErr)
	}

	if r := parseRequirementsSection(output); r != "" {
		requirements = r
	} else {
		// Fallback: treat entire output as requirements.
		requirements = output
	}

	return summary, requirements, nil
}

func (a *PMAgent) completeGatherTurn(ctx context.Context, systemPrompt string, messages []runner.ConvMessage) (string, error) {
	req := runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     messages,
	}

	ch, err := a.runner.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("runner: %w", err)
	}
	output, err := a.collectStream(ch)
	if err != nil {
		return "", fmt.Errorf("stream: %w", err)
	}
	if !looksLikeGatherPromptLeak(output) {
		return output, nil
	}

	retryPrompt := systemPrompt + "\n\nCRITICAL: Your previous reply was invalid because it echoed internal instructions, placeholders, or the wrong language. " +
		"Reply directly to the user in the required language only. Do not mention process steps like Discuss, Summarize, or Requirements. " +
		"Do not output code. Ask concise clarifying question(s) or, if enough information is available, output only the required summary/requirements block."

	ch, err = a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: retryPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     messages,
	})
	if err != nil {
		return "", fmt.Errorf("runner retry: %w", err)
	}
	output, err = a.collectStream(ch)
	if err != nil {
		return "", fmt.Errorf("stream retry: %w", err)
	}
	return output, nil
}

func looksLikeGatherPromptLeak(output string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(output))
	if trimmed == "" {
		return false
	}

	needles := []string{
		"1. **discuss**",
		"2. **summarize**",
		"3. **requirements**",
		"wait for the user to confirm the summary",
		"(brief summary:",
		"(requirement 1",
		"## must have",
		"## should have",
		"## could have",
		"## won't have",
		"what would you like to build or change?",
	}
	for _, needle := range needles {
		if strings.Contains(trimmed, needle) {
			return true
		}
	}

	return strings.Contains(trimmed, "```") || strings.Contains(trimmed, "describe(") || strings.Contains(trimmed, "const ")
}

// parseSummarySection extracts the content between ===SUMMARY=== and ===END===.
func parseSummarySection(output string) string {
	sections := parseSections(output, "SUMMARY")
	return strings.TrimSpace(sections["SUMMARY"])
}

// parseRequirementsSection extracts the content between ===REQUIREMENTS=== and ===END===.
func parseRequirementsSection(output string) string {
	sections := parseSections(output, "REQUIREMENTS")
	return strings.TrimSpace(sections["REQUIREMENTS"])
}

func (a *PMAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	payload, ok := msg.Payload.(PMPayload)
	if !ok {
		return bus.Message{}, fmt.Errorf("pm: unexpected payload type %T", msg.Payload)
	}

	vision, moscow, err := a.generate(ctx, payload)
	if err != nil {
		return bus.Message{}, fmt.Errorf("pm: %w", err)
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
func (a *PMAgent) generate(ctx context.Context, payload PMPayload) (string, string, error) {
	systemPrompt := fmt.Sprintf(prompts.MustLoad("pm-system"), payload.ProjectContext)

	userContent := fmt.Sprintf("Create the product vision and MoSCoW feature prioritization for the following requirements.\n\nRequirements:\n%s", payload.Requirements)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return "", "", fmt.Errorf("runner: %w", err)
	}

	output, usage, err := a.collectPMOutput(ch)
	if err != nil {
		return "", "", fmt.Errorf("stream: %w", err)
	}
	output = normalizePMOutput(output)
	if usage != nil {
		a.emitUsage(*usage)
	}
	a.emitOutput(formatPMDisplay(output))

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

func (a *PMAgent) collectPMOutput(ch <-chan runner.Token) (string, *runner.TokenUsage, error) {
	var sb strings.Builder
	var usage *runner.TokenUsage
	for tok := range ch {
		if tok.Error != nil {
			return sb.String(), usage, tok.Error
		}
		if tok.Done {
			usage = tok.Usage
			break
		}
		sb.WriteString(tok.Text)
	}
	return sb.String(), usage, nil
}

func normalizePMOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return output
	}

	jsonText := strings.TrimSpace(trimmed)
	if strings.HasPrefix(jsonText, "```") {
		lines := strings.Split(jsonText, "\n")
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "```") {
			lines = lines[1:]
			if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "```" {
				lines = lines[:n-1]
			}
			jsonText = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(jsonText), &doc); err != nil {
		return output
	}

	vision, hasVision := lookupMapKey(doc, "VISION")
	moscow, hasMoscow := lookupMapKey(doc, "MOSCOW")
	if !hasVision && !hasMoscow {
		return output
	}

	var parts []string
	if hasVision {
		parts = append(parts, "===VISION===\n"+formatPMSectionBody(vision, 0))
	}
	if hasMoscow {
		parts = append(parts, "===MOSCOW===\n"+formatPMSectionBody(moscow, 0))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func formatPMDisplay(output string) string {
	sections := parseSections(output, "VISION", "MOSCOW")
	vision := strings.TrimSpace(sections["VISION"])
	moscow := strings.TrimSpace(sections["MOSCOW"])
	if vision == "" && moscow == "" {
		return output
	}

	var parts []string
	if vision != "" {
		parts = append(parts, "VISION\n"+vision)
	}
	if moscow != "" {
		parts = append(parts, "MOSCOW\n"+moscow)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func lookupMapKey(doc map[string]any, want string) (any, bool) {
	for k, v := range doc {
		if strings.EqualFold(k, want) {
			return v, true
		}
	}
	return nil, false
}

func formatPMSectionBody(value any, depth int) string {
	indent := strings.Repeat("  ", depth)
	switch v := value.(type) {
	case map[string]any:
		keys := orderedPMKeys(v)
		var lines []string
		for _, key := range keys {
			item := v[key]
			switch child := item.(type) {
			case map[string]any, []any:
				lines = append(lines, fmt.Sprintf("%s- %s", indent, key))
				lines = append(lines, formatPMSectionBody(child, depth+1))
			default:
				lines = append(lines, fmt.Sprintf("%s- %s: %s", indent, key, formatPMScalar(child)))
			}
		}
		return strings.Join(lines, "\n")
	case []any:
		var lines []string
		for _, item := range v {
			switch child := item.(type) {
			case map[string]any, []any:
				lines = append(lines, fmt.Sprintf("%s-", indent))
				lines = append(lines, formatPMSectionBody(child, depth+1))
			default:
				lines = append(lines, fmt.Sprintf("%s- %s", indent, formatPMScalar(child)))
			}
		}
		return strings.Join(lines, "\n")
	default:
		return indent + formatPMScalar(v)
	}
}

func orderedPMKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	order := map[string]int{
		"Problem statement":            0,
		"Target users":                 1,
		"Value proposition":            2,
		"Success criteria":             3,
		"Constraints and assumptions":  4,
		"Existing codebase assessment": 5,
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, iok := order[keys[i]]
		oj, jok := order[keys[j]]
		switch {
		case iok && jok:
			return oi < oj
		case iok:
			return true
		case jok:
			return false
		default:
			return keys[i] < keys[j]
		}
	})
	return keys
}

func formatPMScalar(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
	}
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

// ArbitrateAllResult is the PM's decision on combined review feedback.
type ArbitrateAllResult struct {
	Pass        bool      // true = all clear, nothing to fix
	SubTasks    []SubTask // new beads for real issues (empty when Pass)
	ReviewScope string    // "full", "partial", or "none"
	NiceToHave  []string  // issues PM decided to skip
}

// ArbitrateAll asks the PM to evaluate combined feedback from QA, UX, and
// Security review phases in a single call. PM decides what is real, creates
// sub-tasks for blockers, and determines the scope of re-review.
func (a *PMAgent) ArbitrateAll(ctx context.Context, qaFeedback, uxFeedback, securityFeedback string) (ArbitrateAllResult, error) {
	systemPrompt := prompts.MustLoad("pm-arbitrate-all")

	var b strings.Builder
	if strings.TrimSpace(qaFeedback) != "" {
		fmt.Fprintf(&b, "## QA Review Feedback\n\n%s\n\n", qaFeedback)
	}
	if strings.TrimSpace(uxFeedback) != "" {
		fmt.Fprintf(&b, "## UX/UI Review Feedback\n\n%s\n\n", uxFeedback)
	}
	if strings.TrimSpace(securityFeedback) != "" {
		fmt.Fprintf(&b, "## Security Review Feedback\n\n%s\n\n", securityFeedback)
	}

	if b.Len() == 0 {
		return ArbitrateAllResult{Pass: true, ReviewScope: "none"}, nil
	}

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return ArbitrateAllResult{}, fmt.Errorf("pm arbitrate all: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return ArbitrateAllResult{}, fmt.Errorf("pm arbitrate all: stream: %w", err)
	}

	return parseArbitrateAllResult(output), nil
}

func parseArbitrateAllResult(text string) ArbitrateAllResult {
	result := ArbitrateAllResult{Pass: true, ReviewScope: "none"}

	lines := strings.Split(text, "\n")
	section := ""

	for _, line := range lines {
		upper := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.Contains(upper, "===VERDICT==="):
			section = "verdict"
		case section == "verdict" && strings.Contains(upper, "FIX"):
			result.Pass = false
			section = ""
		case section == "verdict" && strings.Contains(upper, "PASS"):
			result.Pass = true
			section = ""
		case strings.Contains(upper, "===REVIEW_SCOPE==="):
			section = "scope"
		case section == "scope":
			scope := strings.TrimSpace(strings.ToLower(line))
			if scope == "full" || scope == "partial" || scope == "none" {
				result.ReviewScope = scope
			}
			section = ""
		case strings.Contains(upper, "===NICE_TO_HAVE==="):
			section = "nice"
		case strings.Contains(upper, "===TASKS==="):
			section = ""
		default:
			if section == "nice" {
				item := extractListItem(line)
				if item != "" && !isNoneValue(item) {
					result.NiceToHave = append(result.NiceToHave, item)
				}
			}
		}
	}

	// Parse sub-tasks using existing helper.
	if tasks, err := parseSubTasks(text); err == nil && len(tasks) > 0 {
		result.SubTasks = tasks
		result.Pass = false
	}

	return result
}

// TaskSpec is the PM's formalized understanding of a task.
type TaskSpec struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Scope              string   `json:"scope"`
	Pipeline           string   `json:"pipeline,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints,omitempty"`
	FilesToModify      []string `json:"files_to_modify,omitempty"`
}

// Pipeline identifiers PM selects to route a task through the right execution
// strategy. Empty defaults to PipelineGreen after resolution.
const (
	PipelineGreen = "green" // build new from scratch (TDD)
	PipelineBrown = "brown" // existing codebase: research → discuss → TDD
	PipelineFix   = "fix"   // bug/repair: research → short coder-first fix loop
	PipelineRnD   = "rnd"   // proof-of-concept: short PM↔user loop, quick PoC
)

// ExecutionPlan is the PM's strategy for implementing a task.
type ExecutionPlan struct {
	NeedsArchitecture bool   `json:"needs_architecture"`
	NeedsDetailedPlan bool   `json:"needs_detailed_plan"`
	CoderInstructions string `json:"coder_instructions"`
}

// SubTask is one decomposed unit of work that becomes a Beads issue and a
// single coder pass. `Key` is a local identifier (e.g. "T1") used to express
// ordering between sub-tasks via DependsOn before they are persisted as beads.
type SubTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    int      `json:"priority"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// NegotiateTask conducts a multi-turn conversation to formalize a TaskSpec.
// PM emits questions via bus; human responses arrive through humanCh.
// When PM is satisfied, it emits the final TaskSpec.
//
// Negotiation results are cached on disk keyed by sha256(initial input +
// project context). When the same input is replayed (e.g. rerunning the
// orchestrator with an unchanged task description) the cached TaskSpec is
// returned without invoking the LLM. Cache is invalidated automatically when
// either the user input or the project context changes by a single byte.
func (a *PMAgent) NegotiateTask(ctx context.Context, input, projectCtx string, humanCh <-chan string) (TaskSpec, error) {
	cacheKey := negotiateCacheKey(input, projectCtx)
	if spec, ok := a.loadCachedTaskSpec(cacheKey); ok {
		a.emit(bus.MsgEvent, fmt.Sprintf("reusing cached task spec (key=%s)", cacheKey[:8]))
		return spec, nil
	}

	systemPrompt := fmt.Sprintf(prompts.MustLoad("pm-negotiate"), projectCtx)

	messages := []runner.ConvMessage{}
	if strings.TrimSpace(input) != "" {
		messages = append(messages, runner.ConvMessage{Role: "user", Content: input})
	} else {
		// No initial task description provided (e.g. "New Task" from the
		// menu). Wait for the user to actually describe what they want
		// before invoking the model — otherwise PM would invent a task
		// from project context alone.
		select {
		case reply, ok := <-humanCh:
			if !ok {
				return TaskSpec{}, fmt.Errorf("pm negotiate: human channel closed before any input")
			}
			messages = append(messages, runner.ConvMessage{Role: "user", Content: reply})
		case <-ctx.Done():
			return TaskSpec{}, ctx.Err()
		}
	}

	for {
		ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
			SystemPrompt: systemPrompt,
			Skills:       a.skills,
			Model:        a.model,
			Messages:     messages,
		})
		if err != nil {
			return TaskSpec{}, fmt.Errorf("pm negotiate: runner: %w", err)
		}

		output, err := a.collectStream(ch)
		if err != nil {
			return TaskSpec{}, fmt.Errorf("pm negotiate: stream: %w", err)
		}

		// Check if PM produced a TaskSpec.
		if spec, ok := parseTaskSpec(output); ok {
			a.storeCachedTaskSpec(cacheKey, spec)
			return spec, nil
		}

		if shouldForceTaskSpec(messages, output) {
			spec, ok, err := a.forceTaskSpec(ctx, systemPrompt, messages)
			if err != nil {
				return TaskSpec{}, fmt.Errorf("pm negotiate force taskspec: %w", err)
			}
			if ok {
				a.storeCachedTaskSpec(cacheKey, spec)
				return spec, nil
			}
		}

		// PM is asking questions — relay to human via bus.
		a.Bus.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgConversation,
			bus.ConversationPayload{From: "pm", Content: output}))

		// Wait for human reply.
		select {
		case reply, ok := <-humanCh:
			if !ok {
				return TaskSpec{}, fmt.Errorf("pm negotiate: human channel closed")
			}
			messages = append(messages,
				runner.ConvMessage{Role: "assistant", Content: output},
				runner.ConvMessage{Role: "user", Content: reply},
			)
		case <-ctx.Done():
			return TaskSpec{}, ctx.Err()
		}
	}
}

// RnDAction is one PM turn in the R&D pipeline: a message to relay to the user,
// an optional quick coder task (proof-of-concept), and whether PM proposes
// ending the experiment because the concept is confirmed.
type RnDAction struct {
	Message    string
	CoderTask  string
	ProposeEnd bool
}

// RnDTurn runs a single PM turn for the R&D pipeline over the running
// conversation. It returns the parsed action and the raw assistant output so
// the caller can append it to the history.
func (a *PMAgent) RnDTurn(ctx context.Context, projectCtx string, messages []runner.ConvMessage) (RnDAction, string, error) {
	systemPrompt := fmt.Sprintf(prompts.MustLoad("pm-rnd"), projectCtx)
	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     messages,
	})
	if err != nil {
		return RnDAction{}, "", fmt.Errorf("pm rnd: runner: %w", err)
	}
	output, err := a.collectStream(ch)
	if err != nil {
		return RnDAction{}, "", fmt.Errorf("pm rnd: stream: %w", err)
	}
	return parseRnDAction(output), output, nil
}

// parseRnDAction extracts the coder directive and end-proposal marker from a PM
// R&D turn, leaving the rest as the user-facing message.
func parseRnDAction(output string) RnDAction {
	act := RnDAction{}
	rest := output

	if stripped, found := stripMarker(rest, "===PROPOSE_END==="); found {
		act.ProposeEnd = true
		rest = stripped
	}
	if inner, remainder, ok := cutBlock(rest, "===CODER===", "===END==="); ok {
		act.CoderTask = strings.TrimSpace(inner)
		rest = remainder
	}
	act.Message = strings.TrimSpace(rest)
	return act
}

// stripMarker removes the first case-insensitive occurrence of marker from s.
func stripMarker(s, marker string) (string, bool) {
	i := strings.Index(strings.ToUpper(s), strings.ToUpper(marker))
	if i < 0 {
		return s, false
	}
	return s[:i] + s[i+len(marker):], true
}

// cutBlock extracts the text between the first case-insensitive start and end
// markers, returning the inner text and s with the whole block removed. When
// start is present but end is missing, the rest of s after start is taken.
func cutBlock(s, start, end string) (inner, remainder string, ok bool) {
	up := strings.ToUpper(s)
	i := strings.Index(up, strings.ToUpper(start))
	if i < 0 {
		return "", s, false
	}
	innerStart := i + len(start)
	j := strings.Index(up[innerStart:], strings.ToUpper(end))
	if j < 0 {
		return s[innerStart:], s[:i], true
	}
	innerEnd := innerStart + j
	return s[innerStart:innerEnd], s[:i] + s[innerEnd+len(end):], true
}

// taskSpecCacheDir is the workspace subdirectory holding cached negotiations.
const taskSpecCacheDir = "taskspec_cache"

// negotiateCacheKey returns a deterministic cache key for a negotiation
// input + project context pair. Caller-provided inputs are hashed together
// so any change in either invalidates the cache automatically.
func negotiateCacheKey(input, projectCtx string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(input)))
	h.Write([]byte{0})
	h.Write([]byte(strings.TrimSpace(projectCtx)))
	return hex.EncodeToString(h.Sum(nil))
}

// loadCachedTaskSpec returns the cached TaskSpec for the given key, if any.
// Cache misses or read errors are treated as "no cache" — the negotiation
// then runs as usual.
func (a *PMAgent) loadCachedTaskSpec(key string) (TaskSpec, bool) {
	if a.ws.Dir == "" {
		return TaskSpec{}, false
	}
	path := filepath.Join(a.ws.Dir, taskSpecCacheDir, key+".json")
	data, err := os.ReadFile(path) // #nosec G304 -- workspace-controlled cache path under .orchestrator/
	if err != nil {
		return TaskSpec{}, false
	}
	var spec TaskSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return TaskSpec{}, false
	}
	if strings.TrimSpace(spec.Title) == "" {
		return TaskSpec{}, false
	}
	return spec, true
}

// storeCachedTaskSpec persists a TaskSpec under the cache key. Failures are
// logged via the bus but never propagated — caching is a best-effort
// optimisation and must not break the negotiation flow.
func (a *PMAgent) storeCachedTaskSpec(key string, spec TaskSpec) {
	if a.ws.Dir == "" {
		return
	}
	dir := filepath.Join(a.ws.Dir, taskSpecCacheDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		a.emit(bus.MsgEvent, fmt.Sprintf("warning: taskspec cache dir: %v", err))
		return
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), data, 0o600); err != nil {
		a.emit(bus.MsgEvent, fmt.Sprintf("warning: taskspec cache write: %v", err))
	}
}

func (a *PMAgent) forceTaskSpec(ctx context.Context, systemPrompt string, messages []runner.ConvMessage) (TaskSpec, bool, error) {
	forcedMessages := append([]runner.ConvMessage{}, messages...)
	forcedMessages = append(forcedMessages, runner.ConvMessage{
		Role: "user",
		Content: "Using the repository context and the conversation so far, stop asking clarifying questions and emit the TASKSPEC now. " +
			"Make reasonable assumptions where needed and put them in CONSTRAINTS.",
	})

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     forcedMessages,
	})
	if err != nil {
		return TaskSpec{}, false, err
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return TaskSpec{}, false, err
	}

	spec, ok := parseTaskSpec(output)
	return spec, ok, nil
}

func shouldForceTaskSpec(messages []runner.ConvMessage, output string) bool {
	if len(messages) == 0 {
		return false
	}
	if !looksLikeGenericClarification(output) {
		return false
	}
	// The PM prompt forbids generic dismissals. If the model produces one
	// anyway, force a TASKSPEC immediately using whatever the user already
	// said — never trap the user in a "please describe the task" loop.
	return true
}

func looksLikeGenericClarification(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}

	needles := []string{
		"możesz podać bardziej szczegółowe informacje",
		"czy możesz podać bardziej szczegółowe informacje",
		"co dokładnie chcesz zmienić",
		"jakie dokładnie",
		"proszę opisać zadanie",
		"opisz zadanie",
		"podaj więcej szczegółów",
		"provide more details",
		"could you provide more details",
		"what exactly do you want to change",
		"please describe the task",
		"please describe what you want",
		"can you elaborate",
		"what would you like to do",
		"what do you want to do",
	}
	for _, needle := range needles {
		if strings.Contains(trimmed, needle) {
			return true
		}
	}
	return false
}

// PlanTask asks the PM to decide the execution strategy for a task.
// brownfield indicates the target repository already contains source code;
// when true, PM is instructed to plan surgical changes instead of scaffolding.
func (a *PMAgent) PlanTask(ctx context.Context, spec TaskSpec, projectCtx string, brownfield bool) (ExecutionPlan, error) {
	systemPrompt := "You are a Product Manager deciding the execution strategy for a task."

	specText := fmt.Sprintf("Title: %s\nScope: %s\nBrownfield: %t\nDescription:\n%s\nAcceptance Criteria:\n",
		spec.Title, spec.Scope, brownfield, spec.Description)
	for _, ac := range spec.AcceptanceCriteria {
		specText += "- " + ac + "\n"
	}
	if len(spec.Constraints) > 0 {
		specText += "Constraints:\n"
		for _, c := range spec.Constraints {
			specText += "- " + c + "\n"
		}
	}
	if len(spec.FilesToModify) > 0 {
		specText += "Files to modify:\n"
		for _, f := range spec.FilesToModify {
			specText += "- " + f + "\n"
		}
	}

	userContent := fmt.Sprintf(prompts.MustLoad("pm-plan-task"), specText, projectCtx)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: systemPrompt,
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("pm plan task: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return ExecutionPlan{}, fmt.Errorf("pm plan task: stream: %w", err)
	}

	return parseExecutionPlan(output), nil
}

// DecomposeTask asks the PM to break the approved TaskSpec into ordered,
// dependency-aware sub-tasks. Each returned sub-task becomes a Beads issue and
// drives one coder pass downstream. `architecture` is optional approved-design
// context (empty for surgical changes that skip the architect step).
func (a *PMAgent) DecomposeTask(ctx context.Context, spec TaskSpec, architecture, projectCtx string, brownfield bool) ([]SubTask, error) {
	specText := buildDecomposeSpecText(spec, architecture, brownfield)
	userContent := fmt.Sprintf(prompts.MustLoad("pm-decompose"), specText, projectCtx)

	ch, err := a.runner.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: "You are a Project Manager decomposing a task into dependency-ordered sub-tasks.",
		Skills:       a.skills,
		Model:        a.model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		return nil, fmt.Errorf("pm decompose: runner: %w", err)
	}

	output, err := a.collectStream(ch)
	if err != nil {
		return nil, fmt.Errorf("pm decompose: stream: %w", err)
	}

	tasks, err := parseSubTasks(output)
	if err != nil {
		return nil, fmt.Errorf("pm decompose: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("pm decompose: no sub-tasks produced")
	}
	return tasks, nil
}

// buildDecomposeSpecText renders a compact, human-readable view of the spec
// (plus optional approved architecture) for the decomposition prompt.
func buildDecomposeSpecText(spec TaskSpec, architecture string, brownfield bool) string {
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Title: %s\nScope: %s\nBrownfield: %t\nDescription:\n%s\n",
		spec.Title, spec.Scope, brownfield, spec.Description)
	if len(spec.AcceptanceCriteria) > 0 {
		sb.WriteString("Acceptance Criteria:\n")
		for _, ac := range spec.AcceptanceCriteria {
			sb.WriteString("- ")
			sb.WriteString(ac)
			sb.WriteString("\n")
		}
	}
	if len(spec.Constraints) > 0 {
		sb.WriteString("Constraints:\n")
		for _, c := range spec.Constraints {
			sb.WriteString("- ")
			sb.WriteString(c)
			sb.WriteString("\n")
		}
	}
	if len(spec.FilesToModify) > 0 {
		sb.WriteString("Files to modify:\n")
		for _, f := range spec.FilesToModify {
			sb.WriteString("- ")
			sb.WriteString(f)
			sb.WriteString("\n")
		}
	}
	if strings.TrimSpace(architecture) != "" {
		sb.WriteString("\nApproved Architecture:\n")
		sb.WriteString(architecture)
		sb.WriteString("\n")
	}
	return sb.String()
}

// parseSubTasks extracts the JSON sub-task array between the ===TASKS=== and
// ===END=== markers emitted by the pm-decompose prompt. Lenient on surrounding
// whitespace and on missing trailing markers. When the model omits the
// ===TASKS=== marker entirely (e.g. emits the JSON array directly after the
// TASKSPEC block), falls back to extracting the first top-level JSON array
// found in the output.
func parseSubTasks(output string) ([]SubTask, error) {
	const startMarker = "===TASKS==="
	const endMarker = "===END==="

	var body string
	if start := strings.Index(output, startMarker); start >= 0 {
		body = output[start+len(startMarker):]
		if end := strings.Index(body, endMarker); end >= 0 {
			body = body[:end]
		}
	} else {
		// Fallback: look for a bare top-level JSON array in the output.
		// Some models drop the ===TASKS=== marker and emit the array
		// directly after the TASKSPEC block.
		extracted, ok := extractJSONArray(output)
		if !ok {
			return nil, fmt.Errorf("missing ===TASKS=== marker and no JSON array found in output")
		}
		body = extracted
	}
	body = strings.TrimSpace(body)

	// Tolerate the model wrapping the array in a markdown code fence.
	body = strings.TrimPrefix(body, "```json")
	body = strings.TrimPrefix(body, "```")
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)

	var tasks []SubTask
	if err := json.Unmarshal([]byte(body), &tasks); err != nil {
		return nil, fmt.Errorf("invalid TASKS JSON: %w", err)
	}
	for i := range tasks {
		if tasks[i].Priority == 0 {
			tasks[i].Priority = 2
		}
	}
	return tasks, nil
}

// extractJSONArray returns the substring spanning the first top-level JSON
// array (`[ ... ]`) in s, respecting bracket nesting inside strings. Used as
// a last-resort fallback for parseSubTasks when the start marker is missing.
func extractJSONArray(s string) (string, bool) {
	startIdx := strings.IndexByte(s, '[')
	if startIdx < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := startIdx; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[startIdx : i+1], true
			}
		}
	}
	return "", false
}

// parseTaskSpec extracts a TaskSpec from PM output. Returns (spec, true) if found.
func parseTaskSpec(output string) (TaskSpec, bool) {
	sections := parseSections(output, "TASKSPEC")
	content := sections["TASKSPEC"]
	if content == "" {
		return TaskSpec{}, false
	}

	spec := TaskSpec{}
	lines := strings.Split(content, "\n")
	currentField := ""
	var descLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		if strings.HasPrefix(upper, "===END===") {
			break
		}

		switch {
		case strings.HasPrefix(upper, "TITLE:"):
			spec.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:6]))
			currentField = ""
		case strings.HasPrefix(upper, "SCOPE:"):
			scope := strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:6]))
			spec.Scope = strings.ToLower(scope)
			currentField = ""
		case strings.HasPrefix(upper, "PIPELINE:"):
			pipeline := strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:9]))
			spec.Pipeline = strings.ToLower(pipeline)
			currentField = ""
		case strings.HasPrefix(upper, "DESCRIPTION:"):
			currentField = "description"
		case strings.HasPrefix(upper, "ACCEPTANCE_CRITERIA:") || strings.HasPrefix(upper, "ACCEPTANCE CRITERIA:"):
			currentField = "acceptance"
		case strings.HasPrefix(upper, "CONSTRAINTS:"):
			currentField = "constraints"
		case strings.HasPrefix(upper, "FILES_TO_MODIFY:") || strings.HasPrefix(upper, "FILES TO MODIFY:"):
			currentField = "files"
		default:
			item := extractListItem(line)
			switch currentField {
			case "description":
				descLines = append(descLines, line)
			case "acceptance":
				if item != "" {
					spec.AcceptanceCriteria = append(spec.AcceptanceCriteria, item)
				}
			case "constraints":
				if item != "" {
					spec.Constraints = append(spec.Constraints, item)
				}
			case "files":
				if item != "" {
					spec.FilesToModify = append(spec.FilesToModify, item)
				}
			}
		}
	}

	spec.Description = strings.TrimSpace(strings.Join(descLines, "\n"))

	if spec.Title == "" {
		return TaskSpec{}, false
	}

	return spec, true
}

// parseExecutionPlan extracts an ExecutionPlan from PM output.
func parseExecutionPlan(output string) ExecutionPlan {
	sections := parseSections(output, "EXECUTION_PLAN")
	content := sections["EXECUTION_PLAN"]
	if content == "" {
		// Fallback: treat entire output as coder instructions.
		return ExecutionPlan{CoderInstructions: output}
	}

	plan := ExecutionPlan{}
	lines := strings.Split(content, "\n")
	currentField := ""
	var instrLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		if strings.HasPrefix(upper, "===END===") {
			break
		}

		switch {
		case strings.HasPrefix(upper, "NEEDS_ARCHITECTURE:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:19]))
			plan.NeedsArchitecture = strings.EqualFold(val, "true")
			currentField = ""
		case strings.HasPrefix(upper, "NEEDS_DETAILED_PLAN:"):
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, trimmed[:20]))
			plan.NeedsDetailedPlan = strings.EqualFold(val, "true")
			currentField = ""
		case strings.HasPrefix(upper, "CODER_INSTRUCTIONS:"):
			currentField = "instructions"
		default:
			if currentField == "instructions" {
				instrLines = append(instrLines, line)
			}
		}
	}

	plan.CoderInstructions = strings.TrimSpace(strings.Join(instrLines, "\n"))
	return plan
}
