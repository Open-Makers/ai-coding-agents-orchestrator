package tui

import (
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// Markdown styles for the artifact viewer. Headings, list bullets, inline bold,
// and "Label:" prefixes get CRT-palette colours so plans/specs are readable.
var (
	mdH1Style     = lipgloss.NewStyle().Bold(true).Foreground(crt.bright)
	mdH2Style     = lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	mdH3Style     = lipgloss.NewStyle().Foreground(crt.primary)
	mdBulletStyle = lipgloss.NewStyle().Foreground(crt.success)
	mdLabelStyle  = lipgloss.NewStyle().Foreground(crt.dim)
	mdBoldStyle   = lipgloss.NewStyle().Bold(true).Foreground(crt.bright)
)

var (
	mdBoldRe  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdLabelRe = regexp.MustCompile(`^([A-Z][A-Za-z][A-Za-z ]{0,18}):(\s.*)?$`)
)

// styleArtifactContent applies display colouring to already-wrapped artifact
// text. JSON artifacts are syntax-highlighted via chroma; everything else is
// treated as markdown and styled line-by-line. Styling runs AFTER wrapping so
// the wrap width math (which ignores ANSI) stays correct.
func styleArtifactContent(filename, wrapped string) string {
	if strings.HasSuffix(filename, ".json") {
		return highlightCode(wrapped, "JSON", styles.Get("dracula"))
	}
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = styleMarkdownLine(line)
	}
	return strings.Join(lines, "\n")
}

// styleMarkdownLine colours a single (already-wrapped) markdown line, preserving
// any leading indentation added by the wrapper.
func styleMarkdownLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]

	switch {
	case strings.HasPrefix(trimmed, "### "):
		return indent + mdH3Style.Render(trimmed)
	case strings.HasPrefix(trimmed, "## "):
		return indent + mdH2Style.Render(trimmed)
	case strings.HasPrefix(trimmed, "# "):
		return indent + mdH1Style.Render(trimmed)
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
		return indent + mdBulletStyle.Render(trimmed[:1]) + " " + styleMarkdownInline(trimmed[2:])
	}

	// "Label: value" rows (e.g. Priority: 1, Depends on: T1, Scope: refactor).
	if m := mdLabelRe.FindStringSubmatch(trimmed); m != nil {
		return indent + mdLabelStyle.Render(m[1]+":") + styleMarkdownInline(m[2])
	}

	return indent + styleMarkdownInline(trimmed)
}

// styleMarkdownInline renders **bold** spans, stripping the markers.
func styleMarkdownInline(s string) string {
	return mdBoldRe.ReplaceAllStringFunc(s, func(match string) string {
		return mdBoldStyle.Render(match[2 : len(match)-2])
	})
}
