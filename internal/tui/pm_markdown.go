package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Markdown accent colors for chat-rendered agent output (PM negotiation, AI
// chat). These layer on top of the CRT amber base so generated prose reads as
// structured markdown instead of one flat color. (Artifact rendering has its
// own styles in artifact_style.go; mdBoldStyle is shared.)
var (
	chatHeadingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ac3de")).Bold(true) // cyan
	chatMoscowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")).Bold(true) // gold
	chatBulletMark   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")).Bold(true) // green
	chatNumberMark   = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ac3de")).Bold(true) // cyan
	chatLabelStyle   = lipgloss.NewStyle().Foreground(crt.bright).Bold(true)
	chatQuoteStyle   = lipgloss.NewStyle().Foreground(crt.dim).Italic(true)
	chatCodeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ac3de"))
)

// moscowHeaders are MoSCoW section titles the PM emits; they get a distinct
// accent so the prioritization structure stands out.
var moscowHeaders = []string{"must have", "should have", "could have", "won't have", "wont have", "will not have"}

// renderMarkdownChatLine renders a chat message whose content is markdown-ish
// agent prose (PM / AI), applying per-line and inline styling on top of base.
// It mirrors renderWrappedChatLine's prefix/indent/wrapping behaviour so it can
// be swapped in for assistant lines.
func renderMarkdownChatLine(prefix, content string, prefixStyle lipgloss.Style, base lipgloss.Style, width int) string {
	maxContentWidth := width - len(prefix)
	if maxContentWidth < 8 {
		maxContentWidth = 8
	}
	indent := strings.Repeat(" ", len(prefix))

	var out []string
	first := true
	for _, logical := range strings.Split(content, "\n") {
		lineStyle, marker, body := classifyMarkdownLine(logical, base)
		markerW := lipgloss.Width(marker)

		wrapped := wrapLine(body, maxContentWidth-markerW)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for wi, part := range wrapped {
			styled := applyInlineMarkdown(part, lineStyle)
			if marker != "" {
				if wi == 0 {
					styled = marker + styled
				} else {
					styled = strings.Repeat(" ", markerW) + styled
				}
			}
			if first {
				out = append(out, prefixStyle.Render(prefix)+styled)
				first = false
			} else {
				out = append(out, indent+styled)
			}
		}
	}
	return strings.Join(out, "\n")
}

// classifyMarkdownLine inspects a single logical line and returns the base style
// for its text, a pre-styled leading marker (bullet/number/label, may be ""),
// and the remaining body text to wrap.
func classifyMarkdownLine(line string, base lipgloss.Style) (style lipgloss.Style, marker, body string) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(strings.TrimRight(trimmed, ":"))

	switch {
	case trimmed == "":
		return base, "", ""

	// Markdown headings: #, ##, ###
	case strings.HasPrefix(trimmed, "#"):
		return chatHeadingStyle, "", strings.TrimLeft(trimmed, "# ")

	// Section markers like ===VISION=== / ===MOSCOW===
	case strings.HasPrefix(trimmed, "===") && strings.HasSuffix(trimmed, "==="):
		return chatHeadingStyle, "", strings.Trim(trimmed, "= ")

	// MoSCoW headers (with or without a leading ##).
	case isMoscowHeader(lower):
		return chatMoscowStyle, "", trimmed

	// Blockquote
	case strings.HasPrefix(trimmed, ">"):
		return chatQuoteStyle, "", strings.TrimLeft(trimmed, "> ")

	// Bullets: -, *, •
	case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "), strings.HasPrefix(trimmed, "• "):
		return base, chatBulletMark.Render("• "), trimmed[2:]

	// Numbered list: "N. " or "N) "
	case numberedListPrefix(trimmed) > 0:
		n := numberedListPrefix(trimmed)
		return base, chatNumberMark.Render(trimmed[:n]) + " ", strings.TrimSpace(trimmed[n:])

	// "Label: value" lines (e.g. "Priority:", "Problem statement:").
	case isLabelLine(trimmed):
		idx := strings.Index(trimmed, ":")
		return base, chatLabelStyle.Render(trimmed[:idx+1]) + " ", strings.TrimSpace(trimmed[idx+1:])

	default:
		return base, "", trimmed
	}
}

func isMoscowHeader(lower string) bool {
	lower = strings.TrimLeft(lower, "# ")
	for _, h := range moscowHeaders {
		if lower == h || strings.HasPrefix(lower, h+" ") {
			return true
		}
	}
	return false
}

// numberedListPrefix returns the byte length of a leading "N." or "N)" marker
// (including the dot/paren), or 0 when the line is not a numbered item.
func numberedListPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) {
		return 0
	}
	if s[i] == '.' || s[i] == ')' {
		return i + 1
	}
	return 0
}

// isLabelLine reports whether a line looks like "Short Label: value" — a short
// label followed by a colon and content, common in PM output.
func isLabelLine(s string) bool {
	idx := strings.Index(s, ":")
	if idx <= 0 || idx > 40 || idx == len(s)-1 {
		return false
	}
	label := s[:idx]
	if strings.ContainsAny(label, ".!?,") {
		return false
	}
	r := label[0]
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// applyInlineMarkdown styles inline **bold** and `code` spans within a single
// (already width-wrapped) text fragment, base-styling the surrounding text.
func applyInlineMarkdown(s string, base lipgloss.Style) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if strings.HasPrefix(s[i:], "**") {
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				b.WriteString(mdBoldStyle.Render(s[i+2 : i+2+end]))
				i = i + 2 + end + 2
				continue
			}
		}
		if s[i] == '`' {
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				b.WriteString(chatCodeStyle.Render(s[i+1 : i+1+end]))
				i = i + 1 + end + 1
				continue
			}
		}
		next := len(s)
		if j := strings.Index(s[i:], "**"); j >= 0 {
			next = min(next, i+j)
		}
		if j := strings.IndexByte(s[i:], '`'); j >= 0 {
			next = min(next, i+j)
		}
		if next <= i {
			next = i + 1
		}
		b.WriteString(base.Render(s[i:next]))
		i = next
	}
	return b.String()
}
