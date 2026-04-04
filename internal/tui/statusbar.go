package tui

import (
	"os/exec"
	"strings"
)

// StatusBarModel renders a single-line status bar at the bottom.
type StatusBarModel struct {
	branch string
	state  string
	width  int
}

func NewStatusBar(width int) StatusBarModel {
	return StatusBarModel{width: width, state: "idle"}
}

func (m StatusBarModel) WithBranch(b string) StatusBarModel { m.branch = b; return m }
func (m StatusBarModel) WithState(s string) StatusBarModel  { m.state = s; return m }
func (m StatusBarModel) WithWidth(w int) StatusBarModel     { m.width = w; return m }

func (m StatusBarModel) View() string {
	left := ""
	if m.branch != "" {
		left = styleStatusKey.Render(m.branch) + "  "
	}
	left += styleStatusBar.Render("● " + m.state)

	shortcuts := styleStatusBar.Render(
		" Ctrl+R req  Ctrl+G git  Ctrl+C chat  Ctrl+A approve  q quit",
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
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
