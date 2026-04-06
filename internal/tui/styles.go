package tui

import "github.com/charmbracelet/lipgloss"

// Role colors.
var roleColors = map[string]lipgloss.Color{
	"pm":          lipgloss.Color("178"), // gold
	"planner":     lipgloss.Color("86"),  // cyan
	"coder":       lipgloss.Color("69"),  // blue
	"tester":      lipgloss.Color("220"), // yellow
	"reviewer":    lipgloss.Color("141"), // purple
	"ux_reviewer": lipgloss.Color("213"), // pink
	"security":    lipgloss.Color("196"), // red
	"qa":          lipgloss.Color("208"), // orange
	"pr":          lipgloss.Color("82"),  // green
	"system":      lipgloss.Color("245"), // gray
}

func roleColor(role string) lipgloss.Color {
	if c, ok := roleColors[role]; ok {
		return c
	}
	return lipgloss.Color("245")
}

// Panel state styles.
var (
	styleWaiting = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleRunning = lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Bold(true)
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	styleGate    = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
)

// Gate banner shown when pipeline is waiting for human approval.
var styleGateBanner = lipgloss.NewStyle().
	Background(lipgloss.Color("130")).
	Foreground(lipgloss.Color("230")).
	Bold(true).
	Padding(0, 2)

// Conversation line.
var styleConvTimestamp = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

// Status bar.
var styleStatusBar = lipgloss.NewStyle().
	Background(lipgloss.Color("235")).
	Foreground(lipgloss.Color("250")).
	Padding(0, 1)

var styleStatusKey = lipgloss.NewStyle().
	Background(lipgloss.Color("235")).
	Foreground(lipgloss.Color("69")).
	Bold(true).
	Padding(0, 1)
