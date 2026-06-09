package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

// ── Actions ──────────────────────────────────────────────────────────────────

type homeAction int

const (
	homeActionNewTask homeAction = iota
	homeActionRunPipeline
	homeActionResume
	homeActionResumeProject
	homeActionDoneTasks
	homeActionOpenProject
	homeActionGlobalSettings
	homeActionModelMemory
	homeActionSetProjectName
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
	accent:   lipgloss.Color("#7aa2f7"), // blue accent for titles
	green:    lipgloss.Color("#9ece6a"), // green
	dim:      crt.dim,
	bright:   crt.bright,
	gold:     lipgloss.Color("#e0af68"), // warm gold
	cyan:     lipgloss.Color("#2ac3de"), // cyan
	red:      lipgloss.Color("#f7768e"), // coral
	border:   lipgloss.Color("#3b4261"), // slightly brighter border
	activeBg: crt.muted,
	headerBg: crt.panelBg,
	footerBg: crt.panelBg,
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

	// projectValid is false when root is not a real project directory
	// (e.g. home dir or missing project markers). Disables Run, Setup, Clean.
	projectValid bool

	// Cached project info — computed once in NewHomeModel, stable across renders.
	cachedRunner     string
	cachedModel      string
	cachedProject    string
	cachedLanguage   string
	cachedPromptLang string
	cachedBranch     string
	cachedOverrides  []agentOverride

	// Recent projects from ~/.orchestrator/projects.json.
	recentProjects []RecentProject

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
		base := filepath.Base(root)
		if base != "/" && base != "." {
			projectName = base
		}
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

	items := []homeMenuItem{
		{icon: "▶", label: "New Task", desc: "Start a PM chat to define a task — attach existing .md with Ctrl+F", action: homeActionNewTask, key: "Enter"},
		{icon: "📂", label: "Open Project", desc: "Switch to another project directory", action: homeActionOpenProject, key: "o"},
		{icon: "🌐", label: "Global Settings", desc: "Default provider & model (~/.orchestrator/config.yaml)", action: homeActionGlobalSettings, key: "g"},
		{icon: "🧠", label: "Model Memory", desc: "Cap local-model RAM or context size (auto RAM→context per agent)", action: homeActionModelMemory, key: "m"},
		{icon: "⚙", label: "Project Setup", desc: "Per-agent runner & model overrides", action: homeActionSetup, key: "s"},
		{icon: "📛", label: "Project Name", desc: "Set this project's display name (.orchestrator/project.yaml)", action: homeActionSetProjectName, key: "n"},
		{icon: "✦", label: "Reset Artifacts", desc: "Remove generated plans & reports — keeps code, config, and project memory", action: homeActionClean, key: "c"},
		{icon: "⏻", label: "Quit", desc: "Exit orchestrator", action: homeActionQuit, key: "q"},
	}

	// Show completed-task history when there is any.
	if done := orchestrator.DoneTasks(context.Background(), root); len(done) > 0 {
		historyItem := homeMenuItem{
			icon:   "🗸",
			label:  "Done Tasks",
			desc:   fmt.Sprintf("Review %d completed task(s)", len(done)),
			action: homeActionDoneTasks,
			key:    "d",
		}
		items = append([]homeMenuItem{historyItem}, items...)
	}

	// Offer "Resume Project" when planning artifacts exist but no strictly
	// resumable (in-progress) task is detected — the PM re-reads .orchestrator
	// and plans only the remaining work.
	_, strictResumable := orchestrator.Resumable(context.Background(), root)
	if !strictResumable && orchestrator.HasReviewableArtifacts(root) {
		reviewItem := homeMenuItem{
			icon:   "↺",
			label:  "Resume Project",
			desc:   "PM reviews .orchestrator artifacts and plans the remaining work",
			action: homeActionResumeProject,
			key:    "r",
		}
		items = append([]homeMenuItem{reviewItem}, items...)
	}

	// Offer Resume at the top when an interrupted task is detected.
	if rt, ok := orchestrator.Resumable(context.Background(), root); ok {
		resumeItem := homeMenuItem{
			icon:   "↻",
			label:  "Resume Task",
			desc:   "Continue the interrupted task: " + rt.Title,
			action: homeActionResume,
			key:    "Enter",
		}
		items = append([]homeMenuItem{resumeItem}, items...)
	}

	return HomeModel{
		cfg:              cfg,
		root:             root,
		wsPath:           wsPath,
		history:          history,
		projectValid:     isValidProjectRoot(root),
		cachedRunner:     runnerName,
		cachedModel:      modelName,
		cachedProject:    projectName,
		cachedLanguage:   language,
		cachedPromptLang: promptLang,
		cachedBranch:     gitBranch,
		cachedOverrides:  overrides,
		recentProjects:   LoadRecentProjects(),
		items:            items,
		width:            80,
		height:           24,
	}
}

func (m HomeModel) Init() tea.Cmd {
	return nil
}

// ── Update ───────────────────────────────────────────────────────────────────

func (m HomeModel) Update(msg tea.Msg) (HomeModel, tea.Cmd) {
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
			return m, nil
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
			if !m.projectValid && requiresProject(selected) {
				return m, m.selectAction(homeActionOpenProject)
			}
			if selected == homeActionQuit {
				m.confirmQuit = true
			} else {
				return m, m.selectAction(selected)
			}
		case "s", "S":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionSetup)
		case "g", "G":
			return m, m.selectAction(homeActionGlobalSettings)
		case "m", "M":
			return m, m.selectAction(homeActionModelMemory)
		case "n", "N":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionSetProjectName)
		case "o", "O":
			return m, m.selectAction(homeActionOpenProject)
		case "c", "C":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
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
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionNewTask)
		case "2":
			return m, m.selectAction(homeActionOpenProject)
		case "3":
			return m, m.selectAction(homeActionGlobalSettings)
		case "4":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionSetup)
		case "5":
			if !m.projectValid {
				return m, m.selectAction(homeActionOpenProject)
			}
			return m, m.selectAction(homeActionClean)
		case "6":
			m.confirmQuit = true
		}
	}

	// Refresh viewport content after cursor/state change.
	if m.ready {
		m.viewport.SetContent(m.renderContent())
	}

	return m, nil
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
		"◈  O R C H E S T R A T O R  v" + Version,
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

	sep := lipgloss.NewStyle().Foreground(homePalette.border).Render(strings.Repeat("─", contentWidth))

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
		warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		confirmBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(crt.warn).
			Padding(1, 3).
			Render(
				warnStyle.Render("  QUIT ORCHESTRATOR?") + "\n\n" +
					dimStyle.Render("  Press ") +
					lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("y") +
					dimStyle.Render(" or ") +
					lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("Enter") +
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

// ── Block-art pixel font ─────────────────────────────────────────────────────

// blockGlyphs defines each letter as a 4-wide × 5-tall pixel grid.
// '#' = filled pixel, '.' = empty pixel.
var blockGlyphs = map[rune][]string{
	'O': {".##.", "#..#", "#..#", "#..#", ".##."},
	'R': {"###.", "#..#", "###.", "#.#.", "#..#"},
	'C': {".###", "#...", "#...", "#...", ".###"},
	'H': {"#..#", "#..#", "####", "#..#", "#..#"},
	'E': {"####", "#...", "###.", "#...", "####"},
	'S': {"####", "#...", ".##.", "...#", "####"},
	'T': {"####", ".##.", ".##.", ".##.", ".##."},
	'A': {".##.", "#..#", "####", "#..#", "#..#"},
	' ': {"....", "....", "....", "....", "...."},
}

// renderBlockText renders text as chunky pixel-art using Unicode half-block characters.
// 5 pixel rows → 3 display rows via ▀▄█ compositing.
func renderBlockText(text string) []string {
	pixelRows := make([]string, 5)
	for i, ch := range text {
		glyph := blockGlyphs[ch]
		if glyph == nil {
			glyph = blockGlyphs[' ']
		}
		if i > 0 {
			for r := range pixelRows {
				pixelRows[r] += "."
			}
		}
		for r := range pixelRows {
			pixelRows[r] += glyph[r]
		}
	}

	pairs := [][2]int{{0, 1}, {2, 3}, {4, -1}}
	result := make([]string, len(pairs))
	for i, pair := range pairs {
		top := pixelRows[pair[0]]
		bot := ""
		if pair[1] >= 0 {
			bot = pixelRows[pair[1]]
		} else {
			bot = strings.Repeat(".", len(top))
		}
		var sb strings.Builder
		for j := 0; j < len(top); j++ {
			t, b := top[j] == '#', bot[j] == '#'
			switch {
			case t && b:
				sb.WriteRune('█')
			case t:
				sb.WriteRune('▀')
			case b:
				sb.WriteRune('▄')
			default:
				sb.WriteRune(' ')
			}
		}
		result[i] = sb.String()
	}
	return result
}

// ── Logo ─────────────────────────────────────────────────────────────────────

func (m HomeModel) renderLogo(maxW int) string {
	tagStyle := lipgloss.NewStyle().Foreground(homePalette.dim)

	if maxW < 62 {
		titleStyle := lipgloss.NewStyle().Foreground(homePalette.gold).Bold(true)
		return lipgloss.JoinVertical(lipgloss.Center,
			titleStyle.Render("◈ ORCHESTRATOR"),
			tagStyle.Render("AI multi-agent coding pipeline"),
		)
	}

	blockLines := renderBlockText("ORCHESTRATOR")

	glowBright := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd787")).Bold(true)
	glowDim := lipgloss.NewStyle().Foreground(lipgloss.Color("#d7af5f"))

	var lines []string
	for i, bl := range blockLines {
		if i < 2 {
			lines = append(lines, glowBright.Render(bl))
		} else {
			lines = append(lines, glowDim.Render(bl))
		}
	}
	lines = append(lines, tagStyle.Render("       AI-powered multi-agent coding pipeline"))

	return strings.Join(lines, "\n")
}

// ── Pipeline visualisation ───────────────────────────────────────────────────

func (m HomeModel) renderPipeline(maxW int) string {
	agents := []string{"TASK", "PM", "CODE", "TEST", "REVIEW", "UX", "SEC", "QA"}

	arrow := lipgloss.NewStyle().Foreground(crt.dim).Render(" → ")

	var parts []string
	for _, label := range agents {
		color := crt.primary
		if c, ok := pipelineColors[label]; ok {
			color = c
		}
		style := lipgloss.NewStyle().Foreground(color).Bold(true)
		parts = append(parts, style.Render(label))
	}

	pipeline := strings.Join(parts, arrow)

	return lipgloss.PlaceHorizontal(maxW, lipgloss.Center, pipeline)
}

// ── Project Info Card ────────────────────────────────────────────────────────

func (m HomeModel) renderInfoCard(contentWidth int) string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	valueStyle := lipgloss.NewStyle().Foreground(p.bright)
	labelStyle := lipgloss.NewStyle().Foreground(p.dim).Width(12)
	titleStyle := lipgloss.NewStyle().Foreground(p.accent).Bold(true)

	cardW := contentWidth/2 - 2
	if contentWidth < 90 {
		cardW = contentWidth - 4
	}

	var lines []string
	lines = append(lines, titleStyle.Render(" ◈ PROJECT"))
	lines = append(lines, "")

	if !m.projectValid {
		warnStyle := lipgloss.NewStyle().Foreground(p.gold)
		lines = append(lines, warnStyle.Render("  ⚠ No project selected"))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  Press ")+
			lipgloss.NewStyle().Foreground(p.accent).Bold(true).Render("o")+
			dimStyle.Render(" to open a project directory"))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  Current directory is not a"))
		lines = append(lines, dimStyle.Render("  recognized project root."))

		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.gold).
			Padding(0, 1).
			Width(cardW).
			Render(strings.Join(lines, "\n"))
	}

	row := func(label, value string, style lipgloss.Style) {
		lines = append(lines, fmt.Sprintf("  %s %s", labelStyle.Render(label), style.Render(value)))
	}

	row("NAME", m.cachedProject, valueStyle)
	if m.cachedLanguage != "" {
		row("LANG", m.cachedLanguage, valueStyle)
	}
	if m.cachedBranch != "" {
		row("BRANCH", " "+m.cachedBranch, valueStyle)
	}
	row("RUNNER", m.cachedRunner+dimStyle.Render(" (global)"), valueStyle)
	row("MODEL", m.cachedModel+dimStyle.Render(" (global)"), valueStyle)
	row("RESP LANG", m.cachedPromptLang+dimStyle.Render(" (global)"), valueStyle)

	if len(m.cachedOverrides) > 0 {
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  AGENTS:"))
		for _, ov := range m.cachedOverrides {
			info := valueStyle.Render(ov.runner + " / " + ov.model)
			agentStyle := lipgloss.NewStyle().Foreground(roleColor(ov.role))
			lines = append(lines, fmt.Sprintf("   %s %s",
				agentStyle.Render(fmt.Sprintf("%-12s", strings.ToUpper(ov.role))),
				info,
			))
		}
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
	titleStyle := lipgloss.NewStyle().Foreground(p.accent).Bold(true)

	// Each menu item gets its own accent color, keyed by action so dynamic
	// items (Resume, Done Tasks) don't shift colors onto the wrong rows or onto
	// the dim description color.
	itemColorFor := func(action homeAction) lipgloss.Color {
		switch action {
		case homeActionNewTask, homeActionResume:
			return p.green
		case homeActionOpenProject, homeActionResumeProject:
			return p.accent
		case homeActionGlobalSettings:
			return p.cyan
		case homeActionModelMemory:
			return p.gold
		case homeActionSetup:
			return p.red
		case homeActionSetProjectName:
			return p.accent
		case homeActionClean:
			return p.gold
		case homeActionDoneTasks:
			return p.bright
		case homeActionQuit:
			return p.dim
		default:
			return p.bright
		}
	}

	cardW := contentWidth/2 - 2
	if contentWidth < 90 {
		cardW = contentWidth - 4
	}
	innerW := cardW - 6
	if innerW < 20 {
		innerW = 20
	}

	var lines []string
	lines = append(lines, titleStyle.Render(" ◈ ACTIONS"))
	lines = append(lines, "")

	for i, item := range m.items {
		disabled := !m.projectValid && requiresProject(item.action)

		itemColor := itemColorFor(item.action)
		if disabled {
			itemColor = p.dim
		}

		key := dimStyle.Render("[" + item.key + "]")

		desc := item.desc
		if disabled {
			desc = "Open a project first (o)"
		}

		if i == m.cursor {
			label := fmt.Sprintf(" %s %s", item.icon, strings.ToUpper(item.label))
			activeLine := lipgloss.NewStyle().
				Background(p.activeBg).
				Foreground(itemColor).
				Bold(true).
				Width(innerW).
				Render(label)
			activeMarker := lipgloss.NewStyle().Foreground(itemColor).Bold(true)
			lines = append(lines, activeMarker.Render("▸")+activeLine+" "+key)
			lines = append(lines, "  "+dimStyle.Render("  "+desc))
		} else {
			inactiveStyle := lipgloss.NewStyle().Foreground(itemColor)
			label := fmt.Sprintf("  %s %s", item.icon, strings.ToUpper(item.label))
			lines = append(lines, inactiveStyle.Render(label)+"  "+key)
			lines = append(lines, "  "+dimStyle.Render("  "+desc))
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

// ── Recent Projects ──────────────────────────────────────────────────────────

func (m HomeModel) renderHistory() string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	titleStyle := lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(p.bright)
	pathStyle := lipgloss.NewStyle().Foreground(p.dim)
	activeStyle := lipgloss.NewStyle().Foreground(p.green)

	// Show recent projects (excluding current).
	var filtered []RecentProject
	for _, proj := range m.recentProjects {
		if proj.Path != m.root {
			filtered = append(filtered, proj)
		}
	}

	if len(filtered) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, titleStyle.Render("  ◈ RECENT PROJECTS")+"  "+dimStyle.Render("(o to switch)"))

	maxShow := 5
	if len(filtered) < maxShow {
		maxShow = len(filtered)
	}
	for i := 0; i < maxShow; i++ {
		proj := filtered[i]
		exists := dirExists(proj.Path)
		marker := activeStyle.Render("●")
		if !exists {
			marker = dimStyle.Render("○")
		}
		name := nameStyle.Render(proj.Name)
		short := m.shortenProjectPath(proj.Path)
		line := fmt.Sprintf("    %s %s  %s", marker, name, pathStyle.Render(short))
		lines = append(lines, line)
	}
	if len(filtered) > maxShow {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("    … and %d more", len(filtered)-maxShow)))
	}
	return strings.Join(lines, "\n")
}

func (m HomeModel) shortenProjectPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	// Ensure home ends with separator so "/home/user" doesn't match "/home/username".
	homePrefix := home
	if !strings.HasSuffix(homePrefix, string(filepath.Separator)) {
		homePrefix += string(filepath.Separator)
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, homePrefix) {
		return "~" + string(filepath.Separator) + path[len(homePrefix):]
	}
	return path
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
		hint("r", "pipeline"),
		hint("o", "project"),
		hint("g", "global"),
		hint("s", "setup"),
		hint("c", "reset"),
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

	versionTag := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.gold).
		Bold(true).
		Render("v" + Version)

	right := versionTag
	if scrollInfo != "" {
		right = scrollInfo + "  " + versionTag
	}

	gap := m.width - lipglossLen(left) - lipglossLen(right) - 2
	if gap < 0 {
		gap = 0
	}

	return style.Render(left + strings.Repeat(" ", gap) + right)
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
	roles := []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security"}
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

// isValidProjectRoot returns true when root looks like a real project directory.
// It rejects the user's home directory and directories without any project markers.
func isValidProjectRoot(root string) bool {
	if root == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err == nil && filepath.Clean(root) == filepath.Clean(home) {
		return false
	}
	// Check project markers plus .orchestrator workspace.
	// Avoid append(projectMarkers, ...) — it can mutate the shared slice.
	for _, marker := range projectMarkers {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".orchestrator")); err == nil {
		return true
	}
	return false
}

// requiresProject returns true for actions that need a valid project directory.
func requiresProject(action homeAction) bool {
	switch action {
	case homeActionNewTask, homeActionRunPipeline, homeActionResume, homeActionResumeProject, homeActionSetup, homeActionSetProjectName, homeActionClean:
		return true
	case homeActionOpenProject, homeActionGlobalSettings, homeActionModelMemory, homeActionDoneTasks, homeActionQuit:
		return false
	}
	return false
}
