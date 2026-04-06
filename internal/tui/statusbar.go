package tui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

// statusBarShortcut defines a keyboard shortcut displayed in the status bar.
type statusBarShortcut struct {
	key   string
	desc  string
	color lipgloss.Color
}

// statusBarShortcuts defines the shortcut hints with per-action accent colors.
var statusBarShortcuts = []statusBarShortcut{
	{"↑↓", "scroll", lipgloss.Color("#7aa2f7")},      // blue — navigation
	{"Ctrl+R", "req", lipgloss.Color("#9ece6a")},     // green — requirements
	{"Ctrl+G", "git", lipgloss.Color("#73daca")},     // teal — git
	{"Ctrl+C", "chat", lipgloss.Color("#2ac3de")},    // cyan — chat
	{"Ctrl+A", "approve", lipgloss.Color("#9ece6a")}, // green — approve
	{"Ctrl+X", "cancel", lipgloss.Color("#f7768e")},  // coral — cancel
	{"q", "quit", lipgloss.Color("#6b553f")},         // dim — quit
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

	descStyle := lipgloss.NewStyle().
		Background(crt.panelBg).
		Foreground(crt.dim)

	var hints []string
	for _, sc := range statusBarShortcuts {
		keyStyle := lipgloss.NewStyle().
			Background(crt.panelBg).
			Foreground(sc.color).
			Bold(true)
		hints = append(hints, keyStyle.Render(sc.key)+descStyle.Render(" "+sc.desc))
	}

	versionTag := lipgloss.NewStyle().
		Background(crt.panelBg).
		Foreground(lipgloss.Color("#e0af68")).
		Bold(true).
		Render("v" + Version)

	shortcuts := strings.Join(hints, "  ") + "  " + versionTag

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
