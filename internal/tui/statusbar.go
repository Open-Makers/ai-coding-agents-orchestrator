package tui

import (
	"fmt"
	"os/exec"
	"strings"
)

// StatusBarModel renders a single-line status bar at the bottom.
type StatusBarModel struct {
	branch     string
	state      string
	stageInfo  string // e.g. "Stage 2/5: Must Have — Auth"
	runner     string
	model      string
	tokenChars int // cumulative output characters (used to estimate tokens)
	width      int
}

func NewStatusBar(width int) StatusBarModel {
	return StatusBarModel{width: width, state: "idle"}
}

func (m StatusBarModel) WithBranch(b string) StatusBarModel    { m.branch = b; return m }
func (m StatusBarModel) WithState(s string) StatusBarModel     { m.state = s; return m }
func (m StatusBarModel) WithStageInfo(s string) StatusBarModel { m.stageInfo = s; return m }
func (m StatusBarModel) WithWidth(w int) StatusBarModel        { m.width = w; return m }
func (m StatusBarModel) WithRunnerModel(r, mdl string) StatusBarModel {
	m.runner = r
	m.model = mdl
	return m
}

// AddTokenChars adds n output characters to the cumulative counter.
func (m StatusBarModel) AddTokenChars(n int) StatusBarModel {
	m.tokenChars += n
	return m
}

// formatTokens returns a human-friendly token estimate string.
// Uses ~4 chars per token as a rough heuristic.
func formatTokens(chars int) string {
	tokens := chars / 4
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM tok", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fk tok", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d tok", tokens)
	}
}

func (m StatusBarModel) View() string {
	left := ""
	if m.branch != "" {
		left = styleStatusKey.Render(m.branch) + "  "
	}
	left += styleStatusBar.Render("● " + m.state)

	if m.stageInfo != "" {
		left += "  " + styleStatusKey.Render("▸ "+m.stageInfo)
	}

	if m.runner != "" || m.model != "" {
		info := m.runner
		if m.model != "" {
			info += "/" + m.model
		}
		left += "  " + styleStatusKey.Render(info)
	}

	if m.tokenChars > 0 {
		left += "  " + styleStatusKey.Render("⚡ "+formatTokens(m.tokenChars))
	}

	shortcuts := styleStatusBar.Render(
		" ↑↓ scroll  Ctrl+R req  Ctrl+G git  Ctrl+C chat  Ctrl+A approve  Ctrl+X cancel  q quit  v" + Version,
	)

	gap := m.width - lipglossLen(left) - lipglossLen(shortcuts)
	if gap < 0 {
		gap = 0
	}

	return left + strings.Repeat(" ", gap) + shortcuts
}

// lipglossLen approximates the visible width of a rendered string by stripping ANSI escapes.
func lipglossLen(s string) int {
	visible := 0
	inEsc := false
	for _, b := range []byte(s) {
		if b == 0x1b {
			inEsc = true
		}
		if inEsc {
			if b == 'm' {
				inEsc = false
			}
			continue
		}
		visible++
	}
	return visible
}

// GitBranch returns the current git branch name for root, or "" on error.
func GitBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output() //nolint:gosec // G204: args are controlled internally
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
