package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// ── provider ─────────────────────────────────────────────────────────────────

type providerKind int

const (
	providerOpenCode providerKind = iota
	providerClaude
	providerOllama
	providerCodex
)

var providerLabels = []string{"OpenCode", "Claude", "Ollama", "Codex"}

func providerFromString(s string) providerKind {
	switch s {
	case "claude":
		return providerClaude
	case "ollama":
		return providerOllama
	case "codex":
		return providerCodex
	default:
		return providerOpenCode
	}
}

func providerToString(p providerKind) string {
	switch p {
	case providerClaude:
		return "claude"
	case providerOllama:
		return "ollama"
	case providerCodex:
		return "codex"
	default:
		return "opencode"
	}
}

// ── sections within the setup screen ─────────────────────────────────────────

type setupSection int

const (
	sectionProvider setupSection = iota
	sectionModel
	sectionLanguage
	sectionProgLang
	sectionAgentSetup
)

// ── per-agent override ───────────────────────────────────────────────────────

type agentSetupOverride struct {
	runner string
	model  string
}

// ── messages ──────────────────────────────────────────────────────────────────

type setupDoneMsg struct {
	provider       string
	model          string
	promptLanguage string
	progLanguage   string
	agentOverrides map[string]agentSetupOverride
}

type setupCancelledMsg struct{}

type modelsListMsg struct {
	models []string
	err    error
}

type ollamaModelsListMsg struct {
	models []string
	err    error
}

type codexModelsListMsg struct {
	models []string
	err    error
}

type claudeModelsListMsg struct {
	models []string
	err    error
}

// pingResultMsg carries the result of an async model reachability test.
type pingResultMsg struct {
	err error
}

// ── SetupModel ────────────────────────────────────────────────────────────────

type SetupModel struct {
	provider providerKind
	section  setupSection

	models        []string
	modelIdx      int
	modelsLoading bool
	modelsStatus  string

	claudeModels        []string
	claudeModelsLoading bool

	ollamaModels        []string
	ollamaModelsLoading bool

	codexModels        []string
	codexModelsLoading bool

	// Language selection.
	languages   []string
	languageIdx int

	// Programming language selection.
	progLanguages []string
	progLangIdx   int

	// Per-agent configuration.
	agentRoles     []string
	agentOverrides map[string]agentSetupOverride
	agentCursor    int
	agentEditing   bool
	agentEditStep  int          // 0 = pick runner, 1 = pick model
	agentEditProv  providerKind // provider being edited for current agent
	agentEditIdx   int          // model index within the editing provider

	globalOnly bool   // when true, hide agent overrides section
	root       string // project root for prompt export

	spinner  spinner.Model
	viewport viewport.Model
	ready    bool
	scrollX  int // horizontal scroll offset

	promptStatus string // status message after prompt export

	// Model validation state.
	pingTesting bool          // true while ping is in progress
	pingErr     error         // non-nil after a failed ping
	pendingSave *setupDoneMsg // save payload waiting for ping result

	width  int
	height int
}

var setupAgentRoles = []string{"pm", "planner", "coder", "coder_fixer", "tester", "reviewer", "ux_reviewer", "security", "qa"}

func NewSetupModel(currentRunner, currentModel, currentLanguage string, agentCfgs map[string]config.AgentConfig) SetupModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(homePalette.accent)

	languages := config.SupportedLanguages
	langIdx := 0
	if currentLanguage != "" {
		for i, lang := range languages {
			if lang == currentLanguage {
				langIdx = i
				break
			}
		}
	}

	m := SetupModel{
		models:              runner.OpenCodeAvailableModels,
		modelsLoading:       true,
		claudeModels:        runner.ClaudeModels,
		claudeModelsLoading: true,
		ollamaModels:        runner.OllamaPopularModels,
		ollamaModelsLoading: true,
		codexModels:         runner.CodexModels,
		codexModelsLoading:  true,
		languages:           languages,
		languageIdx:         langIdx,
		progLanguages:       config.SupportedProgrammingLanguages,
		agentRoles:          setupAgentRoles,
		agentOverrides:      make(map[string]agentSetupOverride),
		spinner:             sp,
		width:               80,
		height:              24,
	}

	m.provider = providerFromString(currentRunner)

	if currentModel != "" {
		for i, mdl := range m.activeModels() {
			if mdl == currentModel {
				m.modelIdx = i
				break
			}
		}
	}

	// Pre-populate per-agent overrides from current config.
	for _, role := range setupAgentRoles {
		if ac, ok := agentCfgs[role]; ok {
			r := ac.Runner
			mdl := ac.Model
			if r != currentRunner || mdl != currentModel {
				m.agentOverrides[role] = agentSetupOverride{runner: r, model: mdl}
			}
		}
	}

	m.section = sectionProvider
	return m
}

// NewSetupModelWithOverrides creates a setup model pre-populated with explicit
// project-level overrides (from project.yaml), instead of detecting them from
// merged config.
func NewSetupModelWithOverrides(currentRunner, currentModel, currentLanguage, currentProgLang string, projectAgents map[string]config.AgentConfig) SetupModel {
	m := NewSetupModel(currentRunner, currentModel, currentLanguage, nil)
	// Pre-select programming language.
	if currentProgLang != "" {
		for i, pl := range m.progLanguages {
			if pl == currentProgLang {
				m.progLangIdx = i
				break
			}
		}
	}
	for role, ac := range projectAgents {
		ov := agentSetupOverride{}
		if ac.Runner != "" {
			ov.runner = ac.Runner
		}
		if ac.Model != "" {
			ov.model = ac.Model
		}
		if ov.runner != "" || ov.model != "" {
			m.agentOverrides[role] = ov
		}
	}
	// In project setup (non-global), start on the agent overrides section.
	m.section = sectionAgentSetup
	return m
}

func (m SetupModel) activeModels() []string {
	return m.modelsForProvider(m.provider)
}

func (m SetupModel) modelsForProvider(p providerKind) []string {
	switch p {
	case providerClaude:
		return m.claudeModels
	case providerOllama:
		return m.ollamaModels
	case providerCodex:
		return m.codexModels
	default:
		return m.models
	}
}

func (m SetupModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchOpenCodeModels(), fetchOllamaModels(), fetchCodexModels(), fetchClaudeModels())
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m SetupModel) Update(msg tea.Msg) (SetupModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scrollX = 0
		m.syncViewport()

	case spinner.TickMsg:
		if m.isLoadingModels() || m.pingTesting {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case pingResultMsg:
		m.pingTesting = false
		if msg.err != nil {
			m.pingErr = msg.err
			if m.ready {
				m.viewport.SetContent(m.renderContent())
			}
		} else if m.pendingSave != nil {
			saved := *m.pendingSave
			m.pendingSave = nil
			return m, func() tea.Msg { return saved }
		}

	case modelsListMsg:
		m.modelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerOpenCode && m.modelIdx < len(m.models) {
				prevModel = m.models[m.modelIdx]
			}
			m.models = msg.models
			m.modelsStatus = ""
			if m.provider == providerOpenCode {
				m.modelIdx = findModelIndex(m.models, prevModel)
			}
		} else if msg.err != nil {
			m.modelsStatus = fmt.Sprintf("could not fetch models: %v", msg.err)
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case ollamaModelsListMsg:
		m.ollamaModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerOllama && m.modelIdx < len(m.ollamaModels) {
				prevModel = m.ollamaModels[m.modelIdx]
			}
			m.ollamaModels = msg.models
			if m.provider == providerOllama {
				m.modelIdx = findModelIndex(m.ollamaModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case codexModelsListMsg:
		m.codexModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerCodex && m.modelIdx < len(m.codexModels) {
				prevModel = m.codexModels[m.modelIdx]
			}
			m.codexModels = msg.models
			if m.provider == providerCodex {
				m.modelIdx = findModelIndex(m.codexModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case claudeModelsListMsg:
		m.claudeModelsLoading = false
		if msg.err == nil && len(msg.models) > 0 {
			prevModel := ""
			if m.provider == providerClaude && m.modelIdx < len(m.claudeModels) {
				prevModel = m.claudeModels[m.modelIdx]
			}
			m.claudeModels = msg.models
			if m.provider == providerClaude {
				m.modelIdx = findModelIndex(m.claudeModels, prevModel)
			}
		}
		if m.ready {
			m.viewport.SetContent(m.renderContent())
		}

	case tea.KeyMsg:
		var cmd tea.Cmd
		prevSection := m.section
		prevEditing := m.agentEditing
		prevEditStep := m.agentEditStep
		m, cmd = m.handleKey(msg)
		cmds = append(cmds, cmd)

		// Update viewport content after key handling.
		if m.ready {
			m.viewport.SetContent(m.renderContent())

			// If section, editing state, or edit step changed, the handler
			// already called ensureSectionVisible which set the correct
			// YOffset. Re-apply it since SetContent may have adjusted it.
			if m.section != prevSection || m.agentEditing != prevEditing || m.agentEditStep != prevEditStep {
				m.ensureSectionVisible()
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m SetupModel) handleKey(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		return m.startValidation()
	case "esc":
		if m.agentEditing {
			if m.agentEditStep == 1 {
				m.agentEditStep = 0
				return m, nil
			}
			m.agentEditing = false
			return m, nil
		}
		return m, func() tea.Msg { return setupCancelledMsg{} }
	case "[":
		if m.scrollX > 0 {
			m.scrollX -= horizontalScrollStep
			if m.scrollX < 0 {
				m.scrollX = 0
			}
		}
		return m, nil
	case "]":
		m.scrollX += horizontalScrollStep
		return m, nil
	}

	switch m.section {
	case sectionProvider:
		return m.handleProviderKeys(msg)
	case sectionModel:
		return m.handleModelKeys(msg)
	case sectionLanguage:
		return m.handleLanguageKeys(msg)
	case sectionProgLang:
		return m.handleProgLangKeys(msg)
	case sectionAgentSetup:
		return m.handleAgentSetupKeys(msg)
	}
	return m, nil
}

func (m SetupModel) handleProviderKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	prev := m.provider
	switch msg.String() {
	case "left", "h":
		if m.provider > 0 {
			m.provider--
		}
	case "right", "l":
		if int(m.provider) < len(providerLabels)-1 {
			m.provider++
		}
	case "tab", "down", "enter":
		m.section = sectionModel
		m.ensureSectionVisible()
	case "shift+tab":
		if !m.globalOnly {
			m.section = sectionProgLang
			m.progLangIdx = len(m.progLanguages) - 1
		} else {
			m.section = sectionLanguage
			m.languageIdx = len(m.languages) - 1
		}
		m.ensureSectionVisible()
	}
	if m.provider != prev {
		m.modelIdx = 0
		m.pingErr = nil
	}
	return m, nil
}

func (m SetupModel) handleModelKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {

	models := m.activeModels()
	switch msg.String() {
	case "up", "k":
		if m.modelIdx > 0 {
			m.modelIdx--
			m.pingErr = nil
		} else {
			m.section = sectionProvider
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.modelIdx < len(models)-1 {
			m.modelIdx++
			m.pingErr = nil
		}
	case "tab":
		m.section = sectionLanguage
		m.ensureSectionVisible()
	case "shift+tab":
		m.section = sectionProvider
		m.ensureSectionVisible()
	case "enter":
		return m.startValidation()
	}
	return m, nil
}

func (m SetupModel) handleLanguageKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.languageIdx > 0 {
			m.languageIdx--
		} else {
			m.section = sectionModel
			models := m.activeModels()
			if m.modelIdx >= len(models) {
				m.modelIdx = max(0, len(models)-1)
			}
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.languageIdx < len(m.languages)-1 {
			m.languageIdx++
		}
	case "tab":
		if !m.globalOnly {
			m.section = sectionAgentSetup
			m.agentCursor = 0
			m.ensureSectionVisible()
		} else {
			m.section = sectionProvider
			m.ensureSectionVisible()
		}
	case "shift+tab":
		m.section = sectionModel
		models := m.activeModels()
		if m.modelIdx >= len(models) {
			m.modelIdx = max(0, len(models)-1)
		}
		m.ensureSectionVisible()
	case "enter":
		return m.startValidation()
	}
	return m, nil
}

func (m SetupModel) handleProgLangKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.progLangIdx > 0 {
			m.progLangIdx--
		} else {
			m.section = sectionAgentSetup
			m.agentCursor = len(m.agentRoles) - 1
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.progLangIdx < len(m.progLanguages)-1 {
			m.progLangIdx++
		}
	case "tab":
		m.section = sectionProvider
		m.ensureSectionVisible()
	case "shift+tab":
		m.section = sectionAgentSetup
		m.agentCursor = len(m.agentRoles) - 1
		m.ensureSectionVisible()
	case "enter":
		return m.startValidation()
	}
	return m, nil
}

func (m SetupModel) handleAgentSetupKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if m.agentEditing {
		return m.handleAgentEditKeys(msg)
	}

	switch msg.String() {
	case "up", "k":
		if m.agentCursor > 0 {
			m.agentCursor--
		} else {
			m.section = sectionLanguage
			m.ensureSectionVisible()
		}
	case "down", "j":
		if m.agentCursor < len(m.agentRoles)-1 {
			m.agentCursor++
		}
	case "enter":
		m.agentEditing = true
		m.agentEditStep = 0
		role := m.agentRoles[m.agentCursor]
		if ov, ok := m.agentOverrides[role]; ok {
			m.agentEditProv = providerFromString(ov.runner)
		} else {
			m.agentEditProv = m.provider
		}
		m.agentEditIdx = 0
		m.ensureSectionVisible()
	case "backspace", "delete":
		role := m.agentRoles[m.agentCursor]
		delete(m.agentOverrides, role)
	case "p":
		m.exportAgentPrompts()
	case "d":
		m.deleteAgentPrompts()
	case "tab":
		m.section = sectionProgLang
		m.progLangIdx = 0
		m.ensureSectionVisible()
	case "shift+tab":
		m.section = sectionLanguage
		m.languageIdx = len(m.languages) - 1
		m.ensureSectionVisible()
	}
	return m, nil
}

func (m SetupModel) handleAgentEditKeys(msg tea.KeyMsg) (SetupModel, tea.Cmd) {
	if m.agentEditStep == 0 {
		// Step 0: pick runner.
		switch msg.String() {
		case "left", "h":
			if m.agentEditProv > 0 {
				m.agentEditProv--
			}
		case "right", "l":
			if int(m.agentEditProv) < len(providerLabels)-1 {
				m.agentEditProv++
			}
		case "enter":
			m.agentEditStep = 1
			m.agentEditIdx = 0
			// Pre-select current model if exists.
			role := m.agentRoles[m.agentCursor]
			if ov, ok := m.agentOverrides[role]; ok {
				for i, mdl := range m.modelsForProvider(m.agentEditProv) {
					if mdl == ov.model {
						m.agentEditIdx = i
						break
					}
				}
			}
			m.ensureSectionVisible()
		}
		return m, nil
	}

	// Step 1: pick model.
	models := m.modelsForProvider(m.agentEditProv)
	switch msg.String() {
	case "up", "k":
		if m.agentEditIdx > 0 {
			m.agentEditIdx--
		}
	case "down", "j":
		if m.agentEditIdx < len(models)-1 {
			m.agentEditIdx++
		}
	case "enter":
		role := m.agentRoles[m.agentCursor]
		runnerStr := providerToString(m.agentEditProv)
		modelStr := ""
		if m.agentEditIdx < len(models) {
			modelStr = models[m.agentEditIdx]
		}
		m.agentOverrides[role] = agentSetupOverride{runner: runnerStr, model: modelStr}
		m.agentEditing = false
	case "backspace", "delete":
		role := m.agentRoles[m.agentCursor]
		delete(m.agentOverrides, role)
		m.agentEditing = false
	}
	return m, nil
}

// startValidation initiates a ping test for the selected provider/model.
// On success the pending save will be emitted automatically via pingResultMsg.
func (m SetupModel) startValidation() (SetupModel, tea.Cmd) {
	if m.pingTesting {
		return m, nil
	}

	provider, model := m.selectedProviderModel()

	overrides := make(map[string]agentSetupOverride)
	for k, v := range m.agentOverrides {
		overrides[k] = v
	}
	promptLang := "English"
	if m.languageIdx < len(m.languages) {
		promptLang = m.languages[m.languageIdx]
	}
	progLang := ""
	if m.progLangIdx < len(m.progLanguages) {
		progLang = m.progLanguages[m.progLangIdx]
	}

	pending := setupDoneMsg{
		provider:       provider,
		model:          model,
		promptLanguage: promptLang,
		progLanguage:   progLang,
		agentOverrides: overrides,
	}
	m.pingTesting = true
	m.pingErr = nil
	m.pendingSave = &pending

	return m, tea.Batch(m.spinner.Tick, pingModel(provider, model))
}

// selectedProviderModel returns the currently selected provider string and model name.
func (m SetupModel) selectedProviderModel() (string, string) {
	models := m.activeModels()
	provider := providerToString(m.provider)
	model := ""
	if m.modelIdx < len(models) {
		model = models[m.modelIdx]
	}
	return provider, model
}

// pingModel returns a tea.Cmd that tests provider/model reachability.
func pingModel(provider, model string) tea.Cmd {
	return func() tea.Msg {
		return pingResultMsg{err: runner.Ping(provider, model)}
	}
}

// ── Viewport ──────────────────────────────────────────────────────────────────

func (m *SetupModel) syncViewport() {
	vpHeight := m.height - 2 // header + footer
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

// ── View ──────────────────────────────────────────────────────────────────────

func (m SetupModel) View() string {
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

	header := headerStyle.Render("⚙  Global Settings")
	if !m.globalOnly {
		header = headerStyle.Render("⚙  Project Setup")
	}
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

// ── Content ───────────────────────────────────────────────────────────────────

func (m SetupModel) renderContent() string {
	p := homePalette
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(p.bright)
	focusLabelStyle := lipgloss.NewStyle().Bold(true).Foreground(p.accent)
	activeItemStyle := lipgloss.NewStyle().Bold(true).Foreground(p.green)
	inactiveItemStyle := lipgloss.NewStyle().Foreground(p.bright)
	sectionHeaderStyle := lipgloss.NewStyle().Bold(true).Foreground(p.gold)

	contentWidth := m.width - 4
	if contentWidth < 60 {
		contentWidth = 60
	}

	sep := dimStyle.Render(strings.Repeat("─", contentWidth))

	providerCard := m.renderProviderCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle)
	modelCard := m.renderModelCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)
	languageCard := m.renderLanguageCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)

	globalHeader := sectionHeaderStyle.Render("  ◆ Global Defaults") + dimStyle.Render("  — saved to ~/.orchestrator/config.yaml")

	var parts []string
	if !m.globalOnly {
		progLangCard := m.renderProgLangCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)
		agentCard := m.renderAgentCard(contentWidth, labelStyle, focusLabelStyle, activeItemStyle, inactiveItemStyle, dimStyle)
		projectHeader := sectionHeaderStyle.Render("  ◆ Project Overrides") + dimStyle.Render("  — saved to .orchestrator/project.yaml")
		parts = []string{
			"",
			projectHeader,
			agentCard,
			sep,
			progLangCard,
			sep,
			globalHeader,
			providerCard,
			sep,
			modelCard,
			sep,
			languageCard,
		}
	} else {
		parts = []string{
			"",
			globalHeader,
			providerCard,
			sep,
			modelCard,
			sep,
			languageCard,
		}
	}

	parts = append(parts, "")

	content := strings.Join(parts, "\n")

	vpH := m.height - 2
	if vpH < 4 {
		vpH = 4
	}

	natW := maxLineWidth(content)
	if natW <= m.width {
		return lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Top, content)
	}

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

func (m SetupModel) renderProviderCard(contentWidth int, label, focusLabel, active, inactive lipgloss.Style) string {
	p := homePalette

	var providerTabs []string
	for i, lbl := range providerLabels {
		if providerKind(i) == m.provider {
			tab := active.Render("▶ " + lbl)
			if m.section == sectionProvider {
				tab = focusLabel.Render("[ " + lbl + " ]")
			}
			providerTabs = append(providerTabs, tab)
		} else {
			providerTabs = append(providerTabs, inactive.Render("  "+lbl))
		}
	}
	providerLine := "  " + strings.Join(providerTabs, "  ")

	providerLabel := label.Render(" Default Provider")
	if m.section == sectionProvider {
		providerLabel = focusLabel.Render(" ◆ Default Provider")
	}

	var lines []string
	lines = append(lines, providerLabel)
	lines = append(lines, "")
	lines = append(lines, providerLine)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(contentWidth - 2).
		Render(strings.Join(lines, "\n"))
}

func (m SetupModel) renderModelCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	p := homePalette

	var modelLines []string
	modelLabel := label.Render(" Default Model")
	if m.section == sectionModel {
		modelLabel = focusLabel.Render(" ◆ Default Model")
	}
	modelLines = append(modelLines, modelLabel)
	modelLines = append(modelLines, "")

	switch m.provider {
	case providerOpenCode:
		modelLines = append(modelLines, m.viewOpenCodeModels(active, inactive, dim, focusLabel)...)
		if m.modelsStatus != "" {
			warnStyle := lipgloss.NewStyle().Foreground(crt.warn)
			modelLines = append(modelLines, "", warnStyle.Render("  "+m.modelsStatus))
		}
	case providerClaude:
		modelLines = append(modelLines, dim.Render("  Claude CLI (--model flag)"))
		modelLines = append(modelLines, m.viewClaudeModels(active, inactive, dim)...)
	case providerOllama:
		modelLines = append(modelLines, dim.Render("  Ollama local models (REST API)"))
		modelLines = append(modelLines, m.viewOllamaModels(active, inactive, dim)...)
	case providerCodex:
		modelLines = append(modelLines, dim.Render("  Codex CLI (OpenAI)"))
		modelLines = append(modelLines, m.viewCodexModels(active, inactive, dim)...)
	}

	// Model validation status.
	if m.pingTesting {
		modelLines = append(modelLines, "")
		modelLines = append(modelLines, dim.Render("  "+m.spinner.View()+" Testing connection…"))
	} else if m.pingErr != nil {
		warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		modelLines = append(modelLines, "")
		modelLines = append(modelLines, warnStyle.Render("  ✗ "+m.pingErr.Error()))
		modelLines = append(modelLines, dim.Render("  Change selection or press Enter to retry"))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(contentWidth - 2).
		Render(strings.Join(modelLines, "\n"))
}

func (m SetupModel) renderLanguageCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	p := homePalette
	isFocused := m.section == sectionLanguage

	var lines []string
	langLabel := label.Render(" Response Language")
	if isFocused {
		langLabel = focusLabel.Render(" ◆ Response Language")
	}
	lines = append(lines, langLabel)
	lines = append(lines, dim.Render("  Language for LLM responses (code stays in English):"))
	lines = append(lines, "")

	lines = append(lines, renderWindowedList(m.languages, m.languageIdx, isFocused, active, inactive, dim)...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(contentWidth - 2).
		Render(strings.Join(lines, "\n"))
}

func (m SetupModel) renderProgLangCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	p := homePalette
	isFocused := m.section == sectionProgLang

	var lines []string
	plLabel := label.Render(" Programming Language")
	if isFocused {
		plLabel = focusLabel.Render(" ◆ Programming Language")
	}
	lines = append(lines, plLabel)
	lines = append(lines, dim.Render("  Primary language for generated code (overrides auto-detection):"))
	lines = append(lines, "")

	lines = append(lines, renderWindowedList(m.progLanguages, m.progLangIdx, isFocused, active, inactive, dim)...)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(contentWidth - 2).
		Render(strings.Join(lines, "\n"))
}

func (m SetupModel) renderAgentCard(contentWidth int, label, focusLabel, active, inactive, dim lipgloss.Style) string {
	p := homePalette
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)
	cyanStyle := lipgloss.NewStyle().Foreground(p.cyan)

	var agentLines []string
	agentLabel := label.Render(" Agent Overrides")
	if m.section == sectionAgentSetup {
		agentLabel = focusLabel.Render(" ◆ Agent Overrides")
	}
	agentLines = append(agentLines, agentLabel)
	agentLines = append(agentLines, dim.Render("  Override runner & model per agent (this project only):"))
	agentLines = append(agentLines, "")

	for i, role := range m.agentRoles {
		c := roleColor(role)
		rs := lipgloss.NewStyle().Foreground(c).Bold(true)

		display := dim.Render("(default)")
		if ov, ok := m.agentOverrides[role]; ok {
			runnerPart := cyanStyle.Render(ov.runner)
			modelPart := inactive.Render(ov.model)
			if ov.model == "" {
				modelPart = dim.Render("(auto)")
			}
			display = runnerPart + dim.Render(" / ") + modelPart
		}

		// Show indicator if custom prompt exists.
		promptTag := ""
		if m.hasCustomPrompt(role) {
			promptTag = goldStyle.Render(" ✎")
		}

		isActive := m.section == sectionAgentSetup && i == m.agentCursor && !m.agentEditing
		if isActive {
			agentLines = append(agentLines,
				active.Render("  ▶ ")+rs.Render(fmt.Sprintf("%-14s", role))+display+promptTag)
		} else {
			agentLines = append(agentLines,
				"    "+rs.Render(fmt.Sprintf("%-14s", role))+display+promptTag)
		}
	}

	// Prompt status message.
	if m.promptStatus != "" {
		agentLines = append(agentLines, "")
		agentLines = append(agentLines, goldStyle.Render("  "+m.promptStatus))
	}

	// Editing panel.
	if m.agentEditing {
		agentLines = append(agentLines, "")
		role := m.agentRoles[m.agentCursor]

		if m.agentEditStep == 0 {
			agentLines = append(agentLines, goldStyle.Render("  Select runner for "+role+":"))
			agentLines = append(agentLines, "")

			var tabs []string
			for i, lbl := range providerLabels {
				if providerKind(i) == m.agentEditProv {
					tabs = append(tabs, focusLabel.Render("[ "+lbl+" ]"))
				} else {
					tabs = append(tabs, inactive.Render("  "+lbl))
				}
			}
			agentLines = append(agentLines, "  "+strings.Join(tabs, "  "))
			agentLines = append(agentLines, "")
			agentLines = append(agentLines, dim.Render("  ←→ runner  Enter confirm  Esc cancel"))
		} else {
			provLabel := providerLabels[m.agentEditProv]
			agentLines = append(agentLines, goldStyle.Render(fmt.Sprintf("  Select model for %s (%s):", role, provLabel)))

			models := m.modelsForProvider(m.agentEditProv)
			if len(models) == 0 {
				agentLines = append(agentLines, dim.Render("  no models available"))
			} else {
				agentLines = append(agentLines, renderWindowedList(models, m.agentEditIdx, true, active, inactive, dim)...)
			}
			agentLines = append(agentLines, "")
			agentLines = append(agentLines, dim.Render("  ↑↓ navigate  Enter select  Bksp reset  Esc back"))
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(contentWidth - 2).
		Render(strings.Join(agentLines, "\n"))
}

// ── Model list renderers ─────────────────────────────────────────────────────

func (m SetupModel) viewOpenCodeModels(active, inactive, dim, _ lipgloss.Style) []string {
	isModelSection := m.section == sectionModel
	var lines []string

	if m.modelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	// Build labels with cloud/local suffix before windowing.
	labels := make([]string, len(m.models))
	for i, mdl := range m.models {
		suffix := dim.Render("  (local)")
		if strings.Contains(mdl, "/") {
			suffix = dim.Render("  (cloud)")
		}
		labels[i] = mdl + suffix
	}
	lines = append(lines, renderWindowedList(labels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewClaudeModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel

	var lines []string

	if m.claudeModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.claudeModels) == 0 {
		lines = append(lines, dim.Render("  no models found — is claude CLI installed?"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.claudeModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewOllamaModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel
	var lines []string

	if m.ollamaModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.ollamaModels) == 0 {
		lines = append(lines, dim.Render("  no models found — run: ollama pull <model>"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.ollamaModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

func (m SetupModel) viewCodexModels(active, inactive, dim lipgloss.Style) []string {
	isModelSection := m.section == sectionModel

	var lines []string

	if m.codexModelsLoading {
		lines = append(lines, dim.Render("  loading models…"))
		return lines
	}

	if len(m.codexModels) == 0 {
		lines = append(lines, dim.Render("  no models found — check OPENAI_API_KEY"))
		return lines
	}

	lines = append(lines, renderWindowedList(m.codexModels, m.modelIdx, isModelSection, active, inactive, dim)...)
	return lines
}

// ── Footer ───────────────────────────────────────────────────────────────────

func (m SetupModel) renderFooter(style lipgloss.Style) string {
	p := homePalette
	keyStyle := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.accent).
		Bold(true)

	hint := func(k, desc string) string {
		return keyStyle.Render(k) + lipgloss.NewStyle().Background(p.footerBg).Foreground(p.dim).Render(" "+desc)
	}

	var hints []string
	switch {
	case m.agentEditing && m.agentEditStep == 0:
		hints = []string{
			hint("←→", "runner"),
			hint("Enter", "confirm"),
			hint("Esc", "cancel"),
		}
	case m.agentEditing && m.agentEditStep == 1:
		hints = []string{
			hint("↑↓", "navigate"),
			hint("Enter", "select"),
			hint("Bksp", "reset"),
			hint("Esc", "back"),
		}
	case m.section == sectionProvider:
		hints = []string{
			hint("←→", "provider"),
			hint("Tab/↓", "models"),
			hint("[]", "scroll"),
			hint("Ctrl+S", "save"),
			hint("Esc", "back"),
		}
	case m.section == sectionModel:
		hints = []string{
			hint("↑↓", "navigate"),
			hint("Enter", "save"),
			hint("Tab", "language"),
			hint("Ctrl+S", "save"),
			hint("Esc", "back"),
		}
	case m.section == sectionLanguage:
		hints = []string{
			hint("↑↓", "navigate"),
			hint("Enter", "save"),
		}
		if !m.globalOnly {
			hints = append(hints, hint("Tab", "agents"))
		}
		hints = append(hints,
			hint("Ctrl+S", "save"),
			hint("Esc", "back"),
		)
	case m.section == sectionAgentSetup:
		hints = []string{
			hint("↑↓", "navigate"),
			hint("Enter", "configure"),
			hint("p", "export prompt"),
			hint("d", "delete prompt"),
			hint("Bksp", "reset"),
			hint("Tab", "provider"),
			hint("Ctrl+S", "save"),
		}
	}

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

func fetchOpenCodeModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.OpenCodeListModels()
		return modelsListMsg{models: models, err: err}
	}
}

func fetchOllamaModels() tea.Cmd {
	return func() tea.Msg {
		installed, err := runner.OllamaListInstalled()
		return ollamaModelsListMsg{models: installed, err: err}
	}
}

func fetchCodexModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.CodexListModels()
		return codexModelsListMsg{models: models, err: err}
	}
}

func fetchClaudeModels() tea.Cmd {
	return func() tea.Msg {
		models, err := runner.ClaudeListModels()
		return claudeModelsListMsg{models: models, err: err}
	}
}

// findModelIndex returns the index of name in models, or 0 if not found.
func findModelIndex(models []string, name string) int {
	if name == "" {
		return 0
	}
	for i, m := range models {
		if m == name {
			return i
		}
	}
	return 0
}

// isLoadingModels returns true if any model list is still being fetched.
func (m SetupModel) isLoadingModels() bool {
	return m.modelsLoading || m.claudeModelsLoading || m.ollamaModelsLoading || m.codexModelsLoading
}

// ensureSectionVisible scrolls the viewport so the focused section is visible.
func (m *SetupModel) ensureSectionVisible() {
	if !m.ready {
		return
	}

	// Re-render content with current state to get accurate line positions.
	content := m.renderContent()
	m.viewport.SetContent(content)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	var targetLine int
	switch m.section {
	case sectionAgentSetup:
		if m.globalOnly {
			targetLine = findLineContaining(lines, "Agent Overrides")
			break
		}
		if m.agentEditing {
			// Anchor on the editing panel marker so the runner/model picker
			// stays in view instead of scrolling past the top of the card.
			marker := "Select runner for"
			if m.agentEditStep == 1 {
				marker = "Select model for"
			}
			targetLine = findLineContaining(lines, marker)
			if targetLine < 0 {
				targetLine = totalLines - m.viewport.Height
			}
		} else {
			// Agent overrides is the first section in project setup — scroll to top.
			m.viewport.SetYOffset(0)
			return
		}
	case sectionProgLang:
		targetLine = findLineContaining(lines, "Programming Language")
	case sectionProvider:
		targetLine = findLineContaining(lines, "Default Provider")
		if targetLine < 0 {
			// Fallback: global-only mode, provider is at top.
			m.viewport.SetYOffset(0)
			return
		}
	case sectionModel:
		targetLine = findLineContaining(lines, "Default Model")
	case sectionLanguage:
		targetLine = findLineContaining(lines, "Response Language")
	}

	if targetLine < 0 {
		return
	}

	margin := 2
	target := targetLine - margin
	if target < 0 {
		target = 0
	}

	maxOffset := totalLines - m.viewport.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if target > maxOffset {
		target = maxOffset
	}

	m.viewport.SetYOffset(target)
}

// renderWindowedList renders a windowed view of items around cursor.
// When there are more items than listWindowSize, only a window is shown with
// "↑ N more" / "↓ N more" indicators. items are raw label strings.
const listWindowSize = 7

func renderWindowedList(items []string, cursor int, isFocused bool, active, inactive, dim lipgloss.Style) []string {
	if len(items) == 0 {
		return nil
	}

	start, end := 0, len(items)
	if len(items) > listWindowSize {
		half := listWindowSize / 2
		start = cursor - half
		if start < 0 {
			start = 0
		}
		end = start + listWindowSize
		if end > len(items) {
			end = len(items)
			start = end - listWindowSize
			if start < 0 {
				start = 0
			}
		}
	}

	var lines []string
	if start > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("    ↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		sel := isFocused && i == cursor
		marker := "    "
		if i == cursor {
			marker = "  ▶ "
		}
		if sel {
			lines = append(lines, active.Render(marker+items[i]))
		} else {
			lines = append(lines, inactive.Render(marker+items[i]))
		}
	}
	if end < len(items) {
		lines = append(lines, dim.Render(fmt.Sprintf("    ↓ %d more", len(items)-end)))
	}
	return lines
}

// findLineContaining returns the 0-based line index containing substr, or -1.
func findLineContaining(lines []string, substr string) int {
	for i, l := range lines {
		if strings.Contains(l, substr) {
			return i
		}
	}
	return -1
}

// promptsDir returns the project-level prompts override directory.
func (m SetupModel) promptsDir() string {
	if m.root == "" {
		return ""
	}
	return filepath.Join(m.root, artifacts.DirName, prompts.PromptsDirName)
}

// hasCustomPrompt returns true if any prompt override exists for the given role.
func (m SetupModel) hasCustomPrompt(role string) bool {
	dir := m.promptsDir()
	if dir == "" {
		return false
	}
	for _, name := range prompts.PromptsForRole(role) {
		if prompts.OverrideExists(name, dir) {
			return true
		}
	}
	return false
}

// exportAgentPrompts exports the default prompt templates for the currently selected agent.
func (m *SetupModel) exportAgentPrompts() {
	if m.root == "" {
		m.promptStatus = "no project root — cannot export"
		return
	}
	role := m.agentRoles[m.agentCursor]
	promptNames := prompts.PromptsForRole(role)
	if len(promptNames) == 0 {
		m.promptStatus = fmt.Sprintf("no prompts for %s", role)
		return
	}

	destDir := m.promptsDir()
	var exported []string
	for _, name := range promptNames {
		path, err := prompts.ExportPrompt(name, destDir)
		if err != nil {
			m.promptStatus = fmt.Sprintf("error: %v", err)
			return
		}
		exported = append(exported, filepath.Base(path))
	}

	m.promptStatus = fmt.Sprintf("exported %s → .orchestrator/prompts/", strings.Join(exported, ", "))
}

// deleteAgentPrompts removes project-level prompt overrides for the selected agent.
func (m *SetupModel) deleteAgentPrompts() {
	if m.root == "" {
		return
	}
	role := m.agentRoles[m.agentCursor]
	promptNames := prompts.PromptsForRole(role)
	destDir := m.promptsDir()

	for _, name := range promptNames {
		path := filepath.Join(destDir, name+".md")
		_ = os.Remove(path)
	}
	m.promptStatus = fmt.Sprintf("removed custom prompts for %s", role)
}
