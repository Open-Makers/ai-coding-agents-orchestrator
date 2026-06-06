package orchestrator

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// collectProjectFilesFromRoot merges existing files with source files found in root.
func collectProjectFilesFromRoot(existing []string, root string) []string {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		seen[f] = true
	}
	result := make([]string, len(existing))
	copy(result, existing)

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "" || strings.HasPrefix(rel, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if ext == "" {
			return nil
		}
		if !seen[rel] {
			result = append(result, rel)
			seen[rel] = true
		}
		return nil
	})

	return agent.FilterSourceFiles(result)
}

// totalNiceToHave counts all collected nice-to-have items.
func totalNiceToHave(items map[string][]string) int {
	total := 0
	for _, v := range items {
		total += len(v)
	}
	return total
}

// saveNiceToHaveFile writes all collected nice-to-have suggestions to a markdown file.
func saveNiceToHaveFile(ws artifacts.Workspace, items map[string][]string, log *slog.Logger) {
	if len(items) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("# Nice to Have / Recommendations\n\n")
	sb.WriteString("Items deferred from MoSCoW plan and suggestions from review phases.\n\n")

	for _, phase := range []string{"Could Have (from plan)", "Won't Have (from plan)", "Code Review", "UX/UI", "Security"} {
		phaseItems := items[phase]
		if len(phaseItems) == 0 {
			continue
		}
		sb.WriteString("## " + phase + "\n\n")
		for _, item := range phaseItems {
			sb.WriteString("- " + item + "\n")
		}
		sb.WriteString("\n")
	}

	if err := ws.WriteFile(artifacts.NiceToHaveFile, []byte(sb.String())); err != nil {
		log.Warn("failed to write nice-to-have file", slog.String("error", err.Error()))
	}
}

// emitSummary builds and emits a final pipeline summary.
func emitSummary(b *bus.Bus, ws artifacts.Workspace, agents map[bus.AgentRole]agent.Agent, niceToHave map[string][]string, agentDurations map[bus.AgentRole]time.Duration, codingStarted time.Time) {
	var sb strings.Builder
	sb.WriteString("\n════════════════════════════════════════\n")
	sb.WriteString("  PIPELINE COMPLETE — SUMMARY\n")
	sb.WriteString("════════════════════════════════════════\n\n")

	phases := []struct {
		name string
		role bus.AgentRole
		file string
	}{
		{"QA Review", bus.RoleQA, artifacts.ReviewFile},
		{"UX/UI Review", bus.RoleUXReviewer, artifacts.UXReviewFile},
		{"Security Audit", bus.RoleSecurity, artifacts.SecurityReviewFile},
	}

	for _, ph := range phases {
		if _, ok := agents[ph.role]; !ok {
			_, _ = fmt.Fprintf(&sb, "  ○ %s — skipped\n", ph.name)
			continue
		}
		if ws.FileExists(ph.file) {
			_, _ = fmt.Fprintf(&sb, "  ✓ %s — passed\n", ph.name)
		} else {
			_, _ = fmt.Fprintf(&sb, "  ? %s — no output\n", ph.name)
		}
	}

	total := totalNiceToHave(niceToHave)
	if total > 0 {
		_, _ = fmt.Fprintf(&sb, "\n  📋 %d nice-to-have suggestions saved to %s\n", total, artifacts.NiceToHaveFile)
		for phase, items := range niceToHave {
			_, _ = fmt.Fprintf(&sb, "     • %s: %d items\n", phase, len(items))
		}
	}

	sb.WriteString("\n  Artifacts:\n")
	for _, file := range []string{
		artifacts.ReviewFile, artifacts.UXReviewFile,
		artifacts.SecurityReviewFile,
		artifacts.NiceToHaveFile,
	} {
		if ws.FileExists(file) {
			_, _ = fmt.Fprintf(&sb, "    • %s\n", file)
		}
	}

	if len(agentDurations) > 0 {
		sb.WriteString("\n  Agent Durations:\n")
		totalDuration := time.Duration(0)
		for _, role := range []bus.AgentRole{
			bus.RolePM, bus.RoleCoder, bus.RoleQA,
			bus.RoleUXReviewer, bus.RoleSecurity,
		} {
			d, ok := agentDurations[role]
			if !ok || d == 0 {
				continue
			}
			totalDuration += d
			_, _ = fmt.Fprintf(&sb, "    %-12s %s\n", string(role), formatDuration(d))
		}
		if totalDuration > 0 {
			_, _ = fmt.Fprintf(&sb, "    %-12s %s\n", "TOTAL", formatDuration(totalDuration))
		}
		if !codingStarted.IsZero() {
			wallClock := time.Since(codingStarted)
			_, _ = fmt.Fprintf(&sb, "    %-12s %s (since first coder handoff)\n", "WALL CLOCK", formatDuration(wallClock))
		}
	}

	sb.WriteString("\n════════════════════════════════════════\n")

	summary := sb.String()
	_ = ws.WriteFile(artifacts.SummaryFile, []byte(summary))

	b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent,
		bus.TokenPayload{Text: summary, Done: false}))
	b.Publish(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent,
		bus.TokenPayload{Text: "", Done: true}))
}

// extractCoderResult safely extracts CoderResult from a message.
func extractCoderResult(msg bus.Message) agent.CoderResult {
	if cr, ok := msg.Payload.(agent.CoderResult); ok {
		return cr
	}
	return agent.CoderResult{}
}

// mergeFileList returns a combined file list without duplicates.
func mergeFileList(existing, added []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, f := range existing {
		seen[f] = true
	}
	result := make([]string, len(existing))
	copy(result, existing)
	for _, f := range added {
		if !seen[f] {
			result = append(result, f)
			seen[f] = true
		}
	}
	return result
}

// formatDuration formats a duration as human-readable string.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// inferBrownfieldScope picks a non-greenfield scope by scanning the user's
// task description for fix/refactor intent keywords. Defaults to "feature".
// Recognises both English and Polish wording since the orchestrator is used
// in mixed-language conversations.
func inferBrownfieldScope(inputs ...string) string {
	joined := strings.ToLower(strings.Join(inputs, " "))

	bugfixKeywords := []string{
		"fix", "bug", "broken", "repair", "regression", "error", "crash",
		"poprawk", "popraw", "napraw", "błąd", "blad", "usterk",
	}
	for _, kw := range bugfixKeywords {
		if strings.Contains(joined, kw) {
			return "bugfix"
		}
	}

	refactorKeywords := []string{
		"refactor", "restructure", "cleanup", "clean up", "rewrite",
		"refaktor", "uporządk", "uporzadk", "przepisz",
	}
	for _, kw := range refactorKeywords {
		if strings.Contains(joined, kw) {
			return "refactor"
		}
	}

	return "feature"
}

// isProjectScaffolded reports whether root already contains a recognizable
// project manifest. Used to decide if a standalone tester step can produce
// meaningful tests, or whether the coder must scaffold the project first.
func isProjectScaffolded(root string) bool {
	for _, marker := range []string{
		"go.mod", "package.json", "Cargo.toml",
		"pyproject.toml", "requirements.txt",
		"Gemfile", "pom.xml", "build.gradle",
	} {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	return false
}

// bootstrapGoModule creates a minimal go.mod for greenfield Go projects so
// the tester step can write valid imports before the coder runs. Returns the
// inferred module path (or empty when go.mod already exists / non-Go project).
func bootstrapGoModule(root, projectName string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		return "", nil
	}
	module := strings.ToLower(strings.TrimSpace(projectName))
	if module == "" {
		module = filepath.Base(root)
	}
	module = strings.ReplaceAll(module, " ", "-")
	if module == "" || module == "." || module == "/" {
		return "", nil
	}
	content := fmt.Sprintf("module %s\n\ngo 1.24\n", module)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(content), 0o600); err != nil {
		return "", err
	}
	return module, nil
}

// specImpliesGo reports whether the user's task description (title + scope hints)
// targets the Go language. Used to decide whether to bootstrap go.mod for TDD.
func specImpliesGo(spec agent.TaskSpec, configuredLang string) bool {
	if strings.EqualFold(configuredLang, "go") {
		return true
	}
	joined := strings.ToLower(spec.Title + " " + spec.Description + " " + strings.Join(spec.Constraints, " "))
	for _, kw := range []string{" golang", " in go ", " w go ", "języku go", "language go", "język go"} {
		if strings.Contains(" "+joined+" ", kw) {
			return true
		}
	}
	return false
}
