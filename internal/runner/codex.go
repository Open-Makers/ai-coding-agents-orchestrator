package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

// CodexRunner executes Codex CLI with prompt templates.
type CodexRunner struct {
	Binary string
	Env    []string
}

func (r CodexRunner) Plan(ctx *orchestrator.Context) error {
	prompt, err := r.buildPlanPrompt(ctx)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.ImplementationPlanFile, output)
}

func (r CodexRunner) Architecture(ctx *orchestrator.Context) error {
	prompt, err := r.buildArchitecturePrompt(ctx)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.ArchitectureFile, output)
}

func (r CodexRunner) Prompts(ctx *orchestrator.Context) error {
	prompt, err := r.buildPromptsPrompt(ctx)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.PromptsFile, output)
}

func (r CodexRunner) Code(ctx *orchestrator.Context) error {
	prompt, err := r.buildCodePrompt(ctx)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	sections, err := parseSections(string(output), []string{"PATCH", "CHANGES", "TEST_CMDS"})
	if err != nil {
		return err
	}
	if err := ctx.Workspace.WriteFile(artifacts.PatchFile, []byte(sections["PATCH"]+"\n")); err != nil {
		return err
	}
	if err := ctx.Workspace.WriteFile(artifacts.ChangesFile, []byte(sections["CHANGES"]+"\n")); err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.TestCmdsFile, []byte(sections["TEST_CMDS"]+"\n"))
}

func (r CodexRunner) Review(ctx *orchestrator.Context) error {
	prompt, err := r.buildReviewPrompt(ctx)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.ReviewFile, output)
}

func (r CodexRunner) Fix(ctx *orchestrator.Context, failure orchestrator.FailureContext) error {
	prompt, err := r.buildFixPrompt(ctx, failure)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	sections, err := parseSections(string(output), []string{"PATCH", "VERIFY"})
	if err != nil {
		return err
	}
	if err := ctx.Workspace.WriteFile(artifacts.PatchFile, []byte(sections["PATCH"]+"\n")); err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.TestCmdsFile, []byte(sections["VERIFY"]+"\n"))
}

func (r CodexRunner) Docs(ctx *orchestrator.Context) error {
	prompt, err := r.buildDocsPrompt(ctx)
	if err != nil {
		return err
	}
	output, err := r.run(prompt)
	if err != nil {
		return err
	}
	return ctx.Workspace.WriteFile(artifacts.PRDescriptionFile, output)
}

func (r CodexRunner) run(prompt string) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "codex"
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), r.Env...)
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codex failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

func (r CodexRunner) buildPlanPrompt(ctx *orchestrator.Context) (string, error) {
	requirements, err := ctx.Workspace.ReadFile(artifacts.RequirementsFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Requirements: %s
- Architecture: <reference architecture.md>
- Constraints: <e.g. no breaking changes>
- Repository context: <structure / key files>
- Definition of done: <acceptance criteria>
`, globalRules, strings.TrimSpace(string(requirements)))), nil
}

func (r CodexRunner) buildArchitecturePrompt(ctx *orchestrator.Context) (string, error) {
	requirements, err := ctx.Workspace.ReadFile(artifacts.RequirementsFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Requirements: %s
- Request: "Propose architecture for the solution. Keep it clear and actionable. Include an iteration section to suggest options and tradeoffs."
`, globalRules, strings.TrimSpace(string(requirements)))), nil
}

func (r CodexRunner) buildPromptsPrompt(ctx *orchestrator.Context) (string, error) {
	plan, err := ctx.Workspace.ReadFile(artifacts.ImplementationPlanFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Plan: %s
- Request: "Create a single markdown file with prompts for each implementation phase, aligned with this plan."
`, globalRules, strings.TrimSpace(string(plan)))), nil
}

func (r CodexRunner) buildCodePrompt(ctx *orchestrator.Context) (string, error) {
	plan, err := ctx.Workspace.ReadFile(artifacts.ImplementationPlanFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Plan: %s
- File scope: <allowed files>
- Go conventions: <lint / format / test rules>
- Request: "Generate a patch/diff and describe the changes."

OUTPUT FORMAT (required):
===PATCH===
<unified diff>
===CHANGES===
<what and why>
===TEST_CMDS===
<commands, one per line>
`, globalRules, strings.TrimSpace(string(plan)))), nil
}

func (r CodexRunner) buildReviewPrompt(ctx *orchestrator.Context) (string, error) {
	plan, err := ctx.Workspace.ReadFile(artifacts.ImplementationPlanFile)
	if err != nil {
		return "", err
	}
	patch, err := ctx.Workspace.ReadFile(artifacts.PatchFile)
	if err != nil {
		return "", err
	}
	report, err := ctx.Workspace.ReadFile(artifacts.TestReportFile)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Plan: %s
- Diff: %s
- Test results: %s
`, globalRules, strings.TrimSpace(string(plan)), strings.TrimSpace(string(patch)), strings.TrimSpace(string(report)))), nil
}

func (r CodexRunner) buildFixPrompt(ctx *orchestrator.Context, failure orchestrator.FailureContext) (string, error) {
	patch, err := ctx.Workspace.ReadFile(artifacts.PatchFile)
	if err != nil {
		return "", err
	}
	failureText := ""
	if failure.Test != nil {
		failureText = failure.Test.Output
	} else if failure.Review != nil {
		failureText = strings.Join(failure.Review.MustFix, "\n")
	}

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Error: %s
- Current diff: %s
- Goal: "Fix and provide a patch plus verification steps."

OUTPUT FORMAT (required):
===PATCH===
<unified diff>
===VERIFY===
<commands, one per line>
`, globalRules, strings.TrimSpace(failureText), strings.TrimSpace(string(patch)))), nil
}

func (r CodexRunner) buildDocsPrompt(ctx *orchestrator.Context) (string, error) {
	changes, err := ctx.Workspace.ReadFile(artifacts.ChangesFile)
	if err != nil {
		return "", err
	}
	report, err := ctx.Workspace.ReadFile(artifacts.TestReportFile)
	if err != nil {
		return "", err
	}
	review, _ := ctx.Workspace.ReadFile(artifacts.ReviewFile)

	return strings.TrimSpace(fmt.Sprintf(`SYSTEM:
%s

USER:
- Goal: <task>
- Changes: %s
- Tests: %s
- Review notes (optional): %s
`, globalRules, strings.TrimSpace(string(changes)), strings.TrimSpace(string(report)), strings.TrimSpace(string(review)))), nil
}

const globalRules = `- You are working inside a repository. Modify only files within scope.
- Prefer minimal, focused changes.
- Always explain what changed, why, and how to verify.
- If information is missing, make the least invasive assumption and list it.
- Do not refactor unrelated code.
- Use idiomatic Go and standard library where possible.`
