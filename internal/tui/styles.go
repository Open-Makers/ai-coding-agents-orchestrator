package tui

import "github.com/charmbracelet/lipgloss"

// ── CRT Theme ────────────────────────────────────────────────────────────────
// Inspired by phosphor monitor aesthetics: amber / green / cyan monochrome.

// crtTheme defines a monochromatic CRT phosphor palette.
type crtTheme struct {
	primary  lipgloss.Color // main phosphor text
	dim      lipgloss.Color // subdued / secondary text
	bright   lipgloss.Color // high-intensity highlights
	bg       lipgloss.Color // deep CRT black
	border   lipgloss.Color // panel borders
	panelBg  lipgloss.Color // panel background tint
	warn     lipgloss.Color // warning / danger accent
	success  lipgloss.Color // success indicator
	muted    lipgloss.Color // very dim, chrome-level
	scanline lipgloss.Color // scanline stripe tint
}

var themeAmber = crtTheme{
	primary:  lipgloss.Color("#ffc66d"),
	dim:      lipgloss.Color("#6b553f"),
	bright:   lipgloss.Color("#ffe4a8"),
	bg:       lipgloss.Color("#0b0c0b"),
	border:   lipgloss.Color("#3a3020"),
	panelBg:  lipgloss.Color("#0d1117"),
	warn:     lipgloss.Color("#ff6633"),
	success:  lipgloss.Color("#c8b040"),
	muted:    lipgloss.Color("#3a3025"),
	scanline: lipgloss.Color("#111111"),
}

// crt is the active CRT theme used across the TUI.
var crt = themeAmber

// Role colors — each agent gets a distinct accent while keeping CRT aesthetics.
var roleColors = map[string]lipgloss.Color{
	"pm":          lipgloss.Color("#e0af68"), // gold — project manager
	"architect":   lipgloss.Color("#2ac3de"), // cyan — software architect
	"planner":     lipgloss.Color("#7aa2f7"), // soft blue
	"coder":       lipgloss.Color("#9ece6a"), // green
	"coder_fixer": lipgloss.Color("#73b34d"), // darker green — fixer variant
	"tester":      lipgloss.Color("#bb9af7"), // lavender
	"reviewer":    lipgloss.Color("#f7768e"), // coral pink
	"ux_reviewer": lipgloss.Color("#ff9e64"), // orange
	"security":    lipgloss.Color("#e0af68"), // gold
	"pr":          lipgloss.Color("#73daca"), // teal
	"system":      crt.dim,
}

func roleColor(role string) lipgloss.Color {
	if c, ok := roleColors[role]; ok {
		return c
	}
	return crt.dim
}

// pipelineColors maps pipeline step labels to distinct colors for the phase bar.
var pipelineColors = map[string]lipgloss.Color{
	"PM":           roleColors["pm"],
	"ARCHITECT":    roleColors["architect"],
	"ARCHITECTURE": roleColors["architect"],
	"PLAN":         roleColors["planner"],
	"PROMPTS":      roleColors["planner"],
	"CODE":         roleColors["coder"],
	"CODING":       roleColors["coder"],
	"TEST":         roleColors["tester"],
	"TESTING":      roleColors["tester"],
	"REVIEW":       roleColors["reviewer"],
	"REVIEWING":    roleColors["reviewer"],
	"UX":           roleColors["ux_reviewer"],
	"UX_REVIEWING": roleColors["ux_reviewer"],
	"SEC":          roleColors["security"],
	"SECURITY":     roleColors["security"],
	"FIXING":       lipgloss.Color("#ff9e64"), // orange — fix cycle
	"DONE":         lipgloss.Color("#73daca"),
}

// Panel state styles — phosphor monochrome.
var (
	styleWaiting = lipgloss.NewStyle().Foreground(crt.dim)
	styleRunning = lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
	styleFixing  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff9e64")).Bold(true)
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("#73daca")).Bold(true)
	styleError   = lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
	styleGate    = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true)
)

// Gate banner — phosphor highlight.
var styleGateBanner = lipgloss.NewStyle().
	Background(crt.border).
	Foreground(crt.bright).
	Bold(true).
	Padding(0, 2)

// Conversation line.
var styleConvTimestamp = lipgloss.NewStyle().Foreground(crt.dim)

// Status bar — CRT terminal footer.
var styleStatusBar = lipgloss.NewStyle().
	Background(crt.panelBg).
	Foreground(crt.dim).
	Padding(0, 1)

var styleStatusKey = lipgloss.NewStyle().
	Background(crt.panelBg).
	Foreground(crt.primary).
	Bold(true).
	Padding(0, 1)
