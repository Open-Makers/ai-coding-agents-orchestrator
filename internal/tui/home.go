package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// ── Actions ──────────────────────────────────────────────────────────────────

type homeAction int

const (
	homeActionRun homeAction = iota
	homeActionGlobalSettings
	homeActionSetup
	homeActionClean
	homeActionQuit
)

type homeSelectedMsg struct{ action homeAction }

type homeMenuItem struct {
	icon   string
	label  string
	desc   string
	action homeAction
	key    string
}

// ── Color palette (centralised) ──────────────────────────────────────────────

var homePalette = struct {
	accent, green, dim, bright, gold, cyan, red lipgloss.Color
	border, activeBg, headerBg, footerBg        lipgloss.Color
}{
	accent:   lipgloss.Color("69"),
	green:    lipgloss.Color("82"),
	dim:      lipgloss.Color("240"),
	bright:   lipgloss.Color("252"),
	gold:     lipgloss.Color("178"),
	cyan:     lipgloss.Color("86"),
	red:      lipgloss.Color("203"),
	border:   lipgloss.Color("237"),
	activeBg: lipgloss.Color("236"),
	headerBg: lipgloss.Color("234"),
	footerBg: lipgloss.Color("235"),
}

// ── HomeModel ────────────────────────────────────────────────────────────────

// HomeModel is the first screen shown when orchestrator launches.
type HomeModel struct {
	cfg     config.Config
	root    string
	wsPath  string
	cursor  int
	items   []homeMenuItem
	history []string
	width   int
	height  int

	// Cached project info — computed once in NewHomeModel, stable across renders.
	cachedRunner     string
	cachedModel      string
	cachedProject    string
	cachedLanguage   string
	cachedPromptLang string
	cachedBranch     string
	cachedOverrides  []agentOverride

	confirmQuit bool // true when showing quit confirmation
	scrollX     int  // horizontal scroll offset (visible chars)

	viewport viewport.Model
	ready    bool // viewport initialised after first WindowSizeMsg
}

func NewHomeModel(cfg config.Config, root string) HomeModel {
	wsPath := filepath.Join(root, ".orchestrator")
	history := LoadHistory(wsPath)

	// Resolve project info ONCE — no map-iteration randomness on every render.
	runnerName, modelName := detectDefaultRunnerModel(cfg.Agents)
	if runnerName == "" {
		runnerName = "(not set)"
	}
	if modelName == "" {
		modelName = "(not set)"
	}

	projectName := cfg.Project.Name
	if projectName == "" && root != "" {
		projectName = filepath.Base(root)
	}
	if projectName == "" {
		projectName = "(unnamed)"
	}

	language := resolveLanguageFromRoot(root, cfg)
	gitBranch := GitBranch(root)
	overrides := resolveOverrides(cfg.Agents, runnerName, modelName)

	promptLang := cfg.PromptLanguage
	if promptLang == "" {
		promptLang = "English"
	}

	return HomeModel{
		cfg:              cfg,
		root:             root,
		wsPath:           wsPath,
		history:          history,
		cachedRunner:     runnerName,
		cachedModel:      modelName,
		cachedProject:    projectName,
		cachedLanguage:   language,
		cachedPromptLang: promptLang,
		cachedBranch:     gitBranch,
		cachedOverrides:  overrides,
		items: []homeMenuItem{
			{icon: "▶", label: "Run Pipeline", desc: "Select requirements and start agents", action: homeActionRun, key: "Enter"},
			{icon: "🌐", label: "Global Settings", desc: "Default provider & model (~/.orchestrator/config.yaml)", action: homeActionGlobalSettings, key: "g"},
			{icon: "⚙", label: "Project Setup", desc: "Per-agent runner & model overrides", action: homeActionSetup, key: "s"},
			{icon: "✦", label: "Clean Workspace", desc: "Remove artifacts, keep config", action: homeActionClean, key: "c"},
			{icon: "⏻", label: "Quit", desc: "Exit orchestrator", action: homeActionQuit, key: "q"},
		},
		width:  80,
		height: 24,
	}
}

func (m HomeModel) Init() tea.Cmd {
	return nil
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m HomeModel) Update(msg tea.Msg) (HomeModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scrollX = 0 // reset horizontal scroll on resize
		m.syncViewport()

	case tea.KeyMsg:
		// Quit confirmation mode intercepts all keys.
		if m.confirmQuit {
			switch msg.String() {
			case "y", "Y", "enter":
				return m, m.selectAction(homeActionQuit)
			default:
				m.confirmQuit = false
			}
			if m.ready {
				m.viewport.SetContent(m.renderContent())
			}
			return m, tea.Batch(cmds...)
		}

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", " ":
			selected := m.items[m.cursor].action
			if selected == homeActionQuit {
				m.confirmQuit = true
			} else {
				return m, m.selectAction(selected)
			}
		case "s", "S":
			return m, m.selectAction(homeActionSetup)
		case "g", "G":
			return m, m.selectAction(homeActionGlobalSettings)
		case "c", "C":
			return m, m.selectAction(homeActionClean)
		case "q", "Q":
			m.confirmQuit = true
		case "left":
			if m.scrollX > 0 {
				m.scrollX -= horizontalScrollStep
				if m.scrollX < 0 {
					m.scrollX = 0
				}
			}
		case "right":
			m.scrollX += horizontalScrollStep
		case "1":
			return m, m.selectAction(homeActionRun)
		case "2":
			return m, m.selectAction(homeActionGlobalSettings)
		case "3":
			return m, m.selectAction(homeActionSetup)
		case "4":
			return m, m.selectAction(homeActionClean)
		case "5":
			m.confirmQuit = true
		}
	}

	// Refresh viewport content after cursor/state change.
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}

	return m, tea.Batch(cmds...)
}

func (m HomeModel) selectAction(action homeAction) tea.Cmd {
	return func() tea.Msg { return homeSelectedMsg{action: action} }
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m HomeModel) View() string {
	p := homePalette

	headerStyle := lipgloss.NewStyle().
		Background(p.headerBg).
		Foreground(p.accent).
		Bold(true).
		Padding(0, 2).
		Width(m.width)

	footerStyle := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.dim).
		Padding(0, 1).
		Width(m.width)

	header := headerStyle.Render(
		"◆  orchestrator v" + Version,
	)

	footer := m.renderFooter(footerStyle)

	if !m.ready {
		return lipgloss.JoinVertical(lipgloss.Left, header, "", footer)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.viewport.View(),
		footer,
	)
}

// ── Viewport management ──────────────────────────────────────────────────────

func (m *HomeModel) syncViewport() {
	// Reserve 2 lines: header + footer.
	vpHeight := m.height - 2
	if vpHeight < 4 {
		vpHeight = 4
	}
	if !m.ready {
		m.viewport = viewport.New(m.width, vpHeight)
		m.viewport.Style = lipgloss.NewStyle()
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
	}
	m.viewport.SetContent(m.renderContent())
}

// ── Content rendering (inside viewport) ──────────────────────────────────────

func (m HomeModel) renderContent() string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)

	// Use a minimum render width so content stays readable in narrow windows.
	contentWidth := m.width - 4
	minContentWidth := 60
	if contentWidth < minContentWidth {
		contentWidth = minContentWidth
	}

	// Decide layout: two-column (wide) or single-column (narrow).
	wideLayout := contentWidth >= 86

	logoBlock := m.renderLogo(contentWidth)
	pipelineBlock := m.renderPipeline(contentWidth)

	sep := dimStyle.Render(strings.Repeat("─", contentWidth))

	infoBlock := m.renderInfoCard(contentWidth)
	menuBlock := m.renderMenu(contentWidth)

	var mainArea string
	if wideLayout {
		mainArea = lipgloss.JoinHorizontal(lipgloss.Top, infoBlock, "  ", menuBlock)
	} else {
		mainArea = lipgloss.JoinVertical(lipgloss.Left, infoBlock, "", menuBlock)
	}

	// Bottom sections.
	var bottomParts []string
	if hist := m.renderHistory(); hist != "" {
		bottomParts = append(bottomParts, hist)
	}
	if ws := m.workspaceStatus(); ws != "" {
		bottomParts = append(bottomParts, dimStyle.Render("  "+ws))
	}

	parts := []string{
		"",
		logoBlock,
		pipelineBlock,
		sep,
		mainArea,
		sep,
	}
	parts = append(parts, bottomParts...)
	parts = append(parts, "") // trailing space

	content := strings.Join(parts, "\n")

	vpH := m.viewport.Height
	if vpH < 4 {
		vpH = 4
	}

	// Quit confirmation overlay.
	if m.confirmQuit {
		warnStyle := lipgloss.NewStyle().Foreground(homePalette.red).Bold(true)
		confirmBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(homePalette.red).
			Padding(1, 3).
			Render(
				warnStyle.Render("  Quit orchestrator?") + "\n\n" +
					dimStyle.Render("  Press ") +
					lipgloss.NewStyle().Foreground(homePalette.bright).Bold(true).Render("y") +
					dimStyle.Render(" or ") +
					lipgloss.NewStyle().Foreground(homePalette.bright).Bold(true).Render("Enter") +
					dimStyle.Render(" to confirm, any other key to cancel"),
			)
		placeW := m.width
		if placeW < 50 {
			placeW = 50
		}
		placed := lipgloss.Place(placeW, vpH, lipgloss.Center, lipgloss.Center, confirmBox)
		if placeW > m.width {
			return horizontalSlice(placed, m.scrollX, m.width)
		}
		return placed
	}

	// Check if content fits horizontally.
	natW := maxLineWidth(content)
	if natW <= m.width {
		// Content fits — center normally.
		return lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Center, content)
	}

	// Content overflows — apply horizontal scroll.
	scrollX := m.scrollX
	maxScrollX := natW - m.width
	if maxScrollX < 0 {
		maxScrollX = 0
	}
	if scrollX > maxScrollX {
		scrollX = maxScrollX
	}
	return horizontalSlice(content, scrollX, m.width)
}

// ── Logo ─────────────────────────────────────────────────────────────────────

func (m HomeModel) renderLogo(maxW int) string {
	p := homePalette
	logoStyle := lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	tagStyle := lipgloss.NewStyle().Foreground(p.dim).Italic(true)

	logo := []string{
		`                 _               _             _              `,
		`   ___  _ __ ___| |__   ___  ___| |_ _ __ __ _| |_ ___  _ __ `,
		`  / _ \| '__/ __| '_ \ / _ \/ __| __| '__/ _' | __/ _ \| '__|`,
		` | (_) | | | (__| | | |  __/\__ \ |_| | | (_| | || (_) | |   `,
		`  \___/|_|  \___|_| |_|\___||___/\__|_|  \__,_|\__\___/|_|   `,
	}

	compact := maxW < 64
	if compact {
		// Smaller terminals get a simple one-liner.
		return lipgloss.JoinVertical(lipgloss.Center,
			logoStyle.Render("◆ ORCHESTRATOR"),
			tagStyle.Render("AI-powered multi-agent coding pipeline"),
		)
	}

	var lines []string
	for _, l := range logo {
		lines = append(lines, logoStyle.Render(l))
	}
	lines = append(lines, tagStyle.Render("  AI-powered multi-agent coding pipeline"))

	return strings.Join(lines, "\n")
}

// ── Pipeline visualisation ───────────────────────────────────────────────────

func (m HomeModel) renderPipeline(maxW int) string {
	agents := []struct {
		label string
		role  string
	}{
		{"PM", "pm"},
		{"Plan", "planner"},
		{"Code", "coder"},
		{"Test", "tester"},
		{"Review", "reviewer"},
		{"UX", "ux_reviewer"},
		{"Sec", "security"},
		{"QA", "qa"},
		{"PR", "pr"},
	}

	dimArrow := lipgloss.NewStyle().Foreground(homePalette.dim).Render(" → ")

	var parts []string
	for _, ag := range agents {
		c := roleColor(ag.role)
		badge := lipgloss.NewStyle().
			Foreground(c).
			Bold(true).
			Render(ag.label)
		parts = append(parts, badge)
	}

	pipeline := strings.Join(parts, dimArrow)

	// Center within available width.
	return lipgloss.PlaceHorizontal(maxW, lipgloss.Center, pipeline)
}

// ── Project Info Card ────────────────────────────────────────────────────────

func (m HomeModel) renderInfoCard(contentWidth int) string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	valueStyle := lipgloss.NewStyle().Foreground(p.bright)
	labelStyle := lipgloss.NewStyle().Foreground(p.dim).Width(12)
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)
	cyanStyle := lipgloss.NewStyle().Foreground(p.cyan)

	var lines []string
	lines = append(lines, goldStyle.Render(" ◆ Project"))
	lines = append(lines, "")

	row := func(label, value string, style lipgloss.Style) {
		lines = append(lines, fmt.Sprintf("  %s %s", labelStyle.Render(label), style.Render(value)))
	}

	row("name", m.cachedProject, valueStyle)
	if m.cachedLanguage != "" {
		row("language", m.cachedLanguage, valueStyle)
	}
	if m.cachedBranch != "" {
		row("branch", " "+m.cachedBranch, cyanStyle)
	}
	row("runner", m.cachedRunner+dimStyle.Render(" (global)"), valueStyle)
	row("model", m.cachedModel+dimStyle.Render(" (global)"), valueStyle)
	row("response lang", m.cachedPromptLang+dimStyle.Render(" (global)"), valueStyle)

	if len(m.cachedOverrides) > 0 {
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  agents:"))
		for _, ov := range m.cachedOverrides {
			info := valueStyle.Render(ov.runner + " / " + ov.model)
			c := roleColor(ov.role)
			roleStyle := lipgloss.NewStyle().Foreground(c)
			lines = append(lines, fmt.Sprintf("   %s %s",
				roleStyle.Render(fmt.Sprintf("%-12s", ov.role)),
				info,
			))
		}
	}

	cardW := contentWidth/2 - 2
	if contentWidth < 90 {
		cardW = contentWidth - 4
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(cardW).
		Render(strings.Join(lines, "\n"))
}

// ── Menu ─────────────────────────────────────────────────────────────────────

func (m HomeModel) renderMenu(contentWidth int) string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	valueStyle := lipgloss.NewStyle().Foreground(p.bright)
	greenStyle := lipgloss.NewStyle().Foreground(p.green).Bold(true)
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)

	cardW := contentWidth/2 - 2
	if contentWidth < 90 {
		cardW = contentWidth - 4
	}
	// Inner width available for text (card border + padding eat ~4 chars).
	innerW := cardW - 6
	if innerW < 20 {
		innerW = 20
	}

	var lines []string
	lines = append(lines, goldStyle.Render(" ◆ Actions"))
	lines = append(lines, "")

	for i, item := range m.items {
		key := dimStyle.Render("[" + item.key + "]")

		if i == m.cursor {
			label := fmt.Sprintf(" %s %s", item.icon, item.label)
			activeLine := lipgloss.NewStyle().
				Background(p.activeBg).
				Foreground(p.green).
				Bold(true).
				Width(innerW).
				Render(label)
			lines = append(lines, greenStyle.Render("▸")+activeLine+" "+key)
			lines = append(lines, "  "+dimStyle.Render("  "+item.desc))
		} else {
			label := fmt.Sprintf("  %s %s", item.icon, item.label)
			lines = append(lines, valueStyle.Render(label)+"  "+key)
			lines = append(lines, "  "+dimStyle.Render("  "+item.desc))
		}
		if i < len(m.items)-1 {
			lines = append(lines, "")
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(cardW).
		Render(strings.Join(lines, "\n"))
}

// ── History ──────────────────────────────────────────────────────────────────

func (m HomeModel) renderHistory() string {
	if len(m.history) == 0 {
		return ""
	}
	dimStyle := lipgloss.NewStyle().Foreground(homePalette.dim)

	var lines []string
	lines = append(lines, dimStyle.Render("  ◆ Recent Requirements"))
	maxShow := 3
	if len(m.history) < maxShow {
		maxShow = len(m.history)
	}
	for i := 0; i < maxShow; i++ {
		display := m.shortenPath(m.history[i])
		lines = append(lines, dimStyle.Render(fmt.Sprintf("    · %s", display)))
	}
	return strings.Join(lines, "\n")
}

// ── Footer ───────────────────────────────────────────────────────────────────

func (m HomeModel) renderFooter(style lipgloss.Style) string {
	p := homePalette
	keyStyle := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.accent).
		Bold(true)

	hint := func(k, desc string) string {
		return keyStyle.Render(k) + lipgloss.NewStyle().Background(p.footerBg).Foreground(p.dim).Render(" "+desc)
	}

	hints := []string{hint("↑↓", "navigate")}
	if m.width < 80 {
		hints = append(hints, hint("←→", "scroll"))
	}
	hints = append(hints,
		hint("Enter", "select"),
		hint("g", "global"),
		hint("s", "setup"),
		hint("c", "clean"),
		hint("q", "quit"),
	)
	left := strings.Join(hints, "  ")

	scrollInfo := ""
	if m.ready && m.viewport.TotalLineCount() > m.viewport.Height {
		pct := m.viewport.ScrollPercent() * 100
		scrollInfo = lipgloss.NewStyle().
			Background(p.footerBg).
			Foreground(p.dim).
			Render(fmt.Sprintf("%.0f%%", pct))
	}

	gap := m.width - lipglossLen(left) - lipglossLen(scrollInfo) - 2
	if gap < 0 {
		gap = 0
	}

	return style.Render(left + strings.Repeat(" ", gap) + scrollInfo)
}

// ── Data resolution helpers ──────────────────────────────────────────────────

type agentOverride struct {
	role   string
	runner string
	model  string
}

// resolveLanguageFromRoot detects project language from config or filesystem markers.
func resolveLanguageFromRoot(root string, cfg config.Config) string {
	lang := cfg.Project.Language
	if lang != "" {
		return lang
	}
	indicators := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"package.json", "javascript/typescript"},
		{"Cargo.toml", "rust"},
		{"setup.py", "python"},
		{"pyproject.toml", "python"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
	}
	for _, ind := range indicators {
		if _, err := os.Stat(filepath.Join(root, ind.file)); err == nil {
			return ind.lang
		}
	}
	return ""
}

// resolveOverrides returns all agents with their effective runner/model.
func resolveOverrides(agents map[string]config.AgentConfig, defaultRunner, defaultModel string) []agentOverride {
	var overrides []agentOverride
	roles := []string{"pm", "planner", "coder", "tester", "reviewer", "ux_reviewer", "security", "qa", "pr"}
	for _, role := range roles {
		if ac, ok := agents[role]; ok {
			r := ac.Runner
			mdl := ac.Model
			if r == "" {
				r = defaultRunner
			}
			if mdl == "" {
				mdl = defaultModel
			}
			overrides = append(overrides, agentOverride{role: role, runner: r, model: mdl})
		}
	}
	return overrides
}

func (m HomeModel) shortenPath(path string) string {
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		return filepath.Base(path)
	}
	if len(rel) > 50 {
		return "…" + rel[len(rel)-49:]
	}
	return rel
}

func (m HomeModel) workspaceStatus() string {
	wsDir := filepath.Join(m.root, ".orchestrator")
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return "⊘ no workspace"
	}

	artifacts := []struct {
		file  string
		label string
	}{
		{"requirements.md", "requirements"},
		{"architecture.md", "architecture"},
		{"implementation_plan.md", "plan"},
		{"prompts.md", "prompts"},
	}

	var found []string
	for _, a := range artifacts {
		if _, err := os.Stat(filepath.Join(wsDir, a.file)); err == nil {
			found = append(found, a.label)
		}
	}

	if len(found) == 0 {
		return "◇ workspace ready"
	}
	return "◆ artifacts: " + strings.Join(found, ", ")
}
