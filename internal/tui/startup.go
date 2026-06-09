package tui

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/gitclient"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

type startupPhase int

const (
	startupPhaseHome        startupPhase = iota // main menu
	startupPhaseSetup                           // API key / model config
	startupPhaseModulePath                      // Go module path input
	startupPhasePicker                          // file picker (legacy pipeline)
	startupPhaseEditor                          // inline requirements editor (legacy)
	startupPhaseOpenProject                     // project directory browser
	startupPhaseProjectList                     // recent projects + browse
	startupPhaseHistory                         // completed-task history
	startupPhaseModelMemory                     // global RAM/context limit setting
)

// startupModel is a standalone Bubble Tea model shown when the orchestrator is
// launched without a requirements file. It progresses through:
// home → (open project →) (setup →) PM chat | picker → done.
type startupModel struct {
	phase         startupPhase
	projectPicker ProjectPickerModel
	dirBrowser    DirBrowserModel
	taskHistory   TaskHistoryModel
	home          HomeModel
	setup         SetupModel
	modelMemory   modelMemoryModel
	moduleInput   textinput.Model
	picker        PickerModel
	editor        EditorModel
	root          string
	wsDir         string // .orchestrator directory path
	wsReqPath     string // absolute path to write new requirements
	cfg           config.Config
	reqPath       string     // result — non-empty when resolved (legacy pipeline)
	chatMode      bool       // result — true when PM chat-based requirements gathering was selected
	resume        bool       // result — true when the user chose to resume an interrupted task
	reviewProject bool       // result — true when the user chose "Resume Project" (PM review)
	pendingAction homeAction // action to resume after module path input
	width         int
	height        int
}

func newStartupModel(root, wsReqPath string, cfg config.Config) startupModel {
	// Auto-save current project to recent list.
	_ = SaveRecentProject(root)
	return startupModel{
		phase:     startupPhaseHome,
		root:      root,
		wsDir:     filepath.Join(root, artifacts.DirName),
		wsReqPath: wsReqPath,
		cfg:       cfg,
		home:      NewHomeModel(cfg, root),
		width:     80,
		height:    24,
	}
}

func (m startupModel) Init() tea.Cmd {
	return m.home.Init()
}

func (m startupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Propagate resize to the active panel.
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wm.Width, wm.Height
		// Forward WindowSizeMsg to home so it can resize its viewport.
		m.home, _ = m.home.Update(msg)
		switch m.phase {
		case startupPhaseHome:
			// Already handled above via m.home.Update(msg).
		case startupPhaseSetup:
			m.setup, _ = m.setup.Update(msg)
		case startupPhaseModulePath:
			m.moduleInput.Width = wm.Width - 10
		case startupPhasePicker:
			m.picker.SetSize(wm.Width, wm.Height)
		case startupPhaseEditor:
			m.editor.SetSize(wm.Width, wm.Height)
		case startupPhaseOpenProject:
			m.dirBrowser.SetSize(wm.Width, wm.Height)
		case startupPhaseProjectList:
			m.projectPicker, _ = m.projectPicker.Update(msg)
		case startupPhaseHistory:
			m.taskHistory.SetSize(wm.Width, wm.Height)
		case startupPhaseModelMemory:
			m.modelMemory, _ = m.modelMemory.Update(msg)
		}
	}

	switch m.phase {

	case startupPhaseHome:
		switch msg := msg.(type) {
		case homeSelectedMsg:
			switch msg.action {
			case homeActionQuit:
				return m, tea.Quit
			case homeActionNewTask:
				// PM chat-based requirements gathering — skip file picker.
				if m.needsModulePath() {
					m.pendingAction = homeActionNewTask
					return m, m.showModulePathInput()
				}
				cleanWorkspace(m.wsDir, false)
				m.chatMode = true
				return m, tea.Quit
			case homeActionRunPipeline:
				// Legacy pipeline flow: choose or create requirements.md first.
				if m.needsModulePath() {
					m.pendingAction = homeActionRunPipeline
					return m, m.showModulePathInput()
				}
				return m, m.showPicker()
			case homeActionResume:
				// Resume the interrupted task — do NOT clean the workspace
				// (the sub-task plan and run-state must survive).
				m.resume = true
				return m, tea.Quit
			case homeActionResumeProject:
				// PM re-reads .orchestrator artifacts and plans the remaining
				// work. Keep the workspace intact so the PM can read it.
				if m.needsModulePath() {
					m.pendingAction = homeActionResumeProject
					return m, m.showModulePathInput()
				}
				m.reviewProject = true
				return m, tea.Quit
			case homeActionDoneTasks:
				hist := NewTaskHistory(orchestrator.DoneTasks(context.Background(), m.root))
				hist.SetSize(m.width, m.height)
				m.taskHistory = hist
				m.phase = startupPhaseHistory
				return m, m.taskHistory.Init()
			case homeActionOpenProject:
				picker := NewProjectPicker(m.root)
				picker.width, picker.height = m.width, m.height
				m.projectPicker = picker
				m.phase = startupPhaseProjectList
				return m, m.projectPicker.Init()
			case homeActionClean:
				cleanWorkspace(m.wsDir, true)
				m.home = NewHomeModel(m.cfg, m.root)
				// Trigger viewport init + spinner for the fresh model.
				m.home.width, m.home.height = m.width, m.height
				m.home.syncViewport()
				return m, m.home.Init()
			case homeActionSetup:
				currentRunner, currentModel := detectDefaultRunnerModel(m.cfg.Agents)
				// Load project-level overrides from project config file.
				projectCfg := config.LoadProject(m.root)
				projectOverrides := make(map[string]config.AgentConfig)
				if projectCfg.Agents != nil {
					projectOverrides = projectCfg.Agents
				}
				setup := NewSetupModelWithOverrides(currentRunner, currentModel, m.cfg.PromptLanguage, m.cfg.Project.Language, m.cfg.Project.Name, projectOverrides)
				setup.root = m.root
				setup.width, setup.height = m.width, m.height
				setup.syncViewport()
				m.setup = setup
				m.phase = startupPhaseSetup
				return m, m.setup.Init()
			case homeActionGlobalSettings:
				currentRunner, currentModel := detectDefaultRunnerModel(m.cfg.Agents)
				setup := NewSetupModel(currentRunner, currentModel, m.cfg.PromptLanguage, m.cfg.Agents)
				setup.globalOnly = true
				setup.width, setup.height = m.width, m.height
				setup.syncViewport()
				m.setup = setup
				m.phase = startupPhaseSetup
				return m, m.setup.Init()
			case homeActionModelMemory:
				mm := newModelMemoryModel(m.cfg)
				mm.width, mm.height = m.width, m.height
				m.modelMemory = mm
				m.phase = startupPhaseModelMemory
				return m, m.modelMemory.Init()
			}
		}
		var cmd tea.Cmd
		m.home, cmd = m.home.Update(msg)
		return m, cmd

	case startupPhaseSetup:
		switch msg := msg.(type) {
		case setupDoneMsg:
			if m.setup.globalOnly {
				// Global Settings: save provider+model+language for all agents.
				globalAgents := make(map[string]config.AgentConfig)
				for role, ac := range m.cfg.Agents {
					ac.Runner = msg.provider
					if msg.model != "" {
						ac.Model = msg.model
					}
					globalAgents[role] = ac
				}
				_ = config.SaveGlobal(config.Config{
					Agents:         globalAgents,
					PromptLanguage: msg.promptLanguage,
				})
				m.cfg.PromptLanguage = msg.promptLanguage

				// Update runtime config with global values.
				for role := range m.cfg.Agents {
					m.cfg.Agents[role] = globalAgents[role]
				}

				// Re-apply existing project overrides on top.
				projectCfg := config.LoadProject(m.root)
				if projectCfg.Agents != nil {
					for role, ov := range projectCfg.Agents {
						if ac, ok := m.cfg.Agents[role]; ok {
							if ov.Runner != "" {
								ac.Runner = ov.Runner
							}
							if ov.Model != "" {
								ac.Model = ov.Model
							}
							m.cfg.Agents[role] = ac
						}
					}
				}
			} else {
				// Project Setup: only save per-agent overrides, don't touch global config.
				if reloaded, ok := applyProjectSetup(m.root, msg); ok {
					m.cfg = reloaded
				}
			}

			// Refresh home with updated cfg, return to home.
			m.home = NewHomeModel(m.cfg, m.root)
			m.home.width, m.home.height = m.width, m.height
			m.home.syncViewport()
			m.phase = startupPhaseHome
			return m, m.home.Init()
		case setupCancelledMsg:
			m.phase = startupPhaseHome
			return m, m.home.Init()
		}
		var cmd tea.Cmd
		m.setup, cmd = m.setup.Update(msg)
		return m, cmd

	case startupPhaseModulePath:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				val := strings.TrimSpace(m.moduleInput.Value())
				if val != "" {
					m.cfg.Project.ModulePath = val
					projectCfg := config.LoadProject(m.root)
					projectCfg.Project.ModulePath = val
					_ = config.Save(m.root, projectCfg)
					return m, m.resumePendingAction()
				}
			case "esc":
				m.phase = startupPhaseHome
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.moduleInput, cmd = m.moduleInput.Update(msg)
		return m, cmd

	case startupPhasePicker:
		switch msg := msg.(type) {
		case PickerSelectedMsg:
			if msg.IsNew {
				m.editor = NewEditor("")
				m.editor.SetSize(m.width, m.height)
				m.phase = startupPhaseEditor
				return m, m.editor.Init()
			}
			if msg.Path != "" {
				m.reqPath = msg.Path
				m.cleanIfRequirementsChanged(msg.Path)
				return m, tea.Quit
			}
		case PickerCancelledMsg:
			m.phase = startupPhaseHome
			return m, m.home.Init()
		}
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd

	case startupPhaseEditor:
		switch msg := msg.(type) {
		case RequirementsSavedMsg:
			content := msg.Path // naming quirk: Path holds textarea content
			m.cleanAfterEditorSave(content)
			_ = os.MkdirAll(filepath.Dir(m.wsReqPath), 0o750)
			_ = os.WriteFile(m.wsReqPath, []byte(content), 0o600)
			m.reqPath = m.wsReqPath
			return m, tea.Quit
		case EditorCancelledMsg:
			// Back to picker.
			m.phase = startupPhasePicker
			return m, nil
		}
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd

	case startupPhaseProjectList:
		switch msg := msg.(type) {
		case ProjectSelectedMsg:
			return m, m.switchProject(msg.Path)
		case ProjectPickerCancelledMsg:
			m.phase = startupPhaseHome
			return m, m.home.Init()
		}
		var cmd tea.Cmd
		m.projectPicker, cmd = m.projectPicker.Update(msg)
		return m, cmd

	case startupPhaseOpenProject:
		switch msg := msg.(type) {
		case DirSelectedMsg:
			return m, m.switchProject(msg.Path)
		case DirBrowserCancelledMsg:
			m.phase = startupPhaseHome
			return m, m.home.Init()
		}
		var cmd tea.Cmd
		m.dirBrowser, cmd = m.dirBrowser.Update(msg)
		return m, cmd

	case startupPhaseHistory:
		switch msg.(type) {
		case TaskHistoryClosedMsg:
			m.phase = startupPhaseHome
			return m, m.home.Init()
		}
		var cmd tea.Cmd
		m.taskHistory, cmd = m.taskHistory.Update(msg)
		return m, cmd

	case startupPhaseModelMemory:
		switch msg := msg.(type) {
		case modelMemoryDoneMsg:
			_ = config.SaveGlobal(config.Config{ModelMemory: msg.cfg})
			m.cfg.ModelMemory = msg.cfg
			m.phase = startupPhaseHome
			m.home = NewHomeModel(m.cfg, m.root)
			m.home.width, m.home.height = m.width, m.height
			m.home.syncViewport()
			return m, m.home.Init()
		case modelMemoryCancelledMsg:
			m.phase = startupPhaseHome
			return m, m.home.Init()
		}
		var cmd tea.Cmd
		m.modelMemory, cmd = m.modelMemory.Update(msg)
		return m, cmd
	}

	return m, nil
}

// cleanIfRequirementsChanged removes generated artifacts only when
// the incoming requirements differ from the previously stored ones.
// If the content is the same, artifacts are preserved for resume.
func (m *startupModel) cleanIfRequirementsChanged(newReqPath string) {
	ws := artifacts.Workspace{Dir: m.wsDir}

	newContent, err := safefile.ReadFile(filepath.Dir(newReqPath), filepath.Base(newReqPath))
	if err != nil {
		return
	}

	oldContent, err := ws.ReadFile(artifacts.RequirementsFile)
	if err != nil {
		// No previous requirements — first run, nothing to clean.
		return
	}

	if strings.TrimSpace(string(oldContent)) == strings.TrimSpace(string(newContent)) {
		return
	}

	ws.CleanGeneratedArtifacts()
}

// cleanAfterEditorSave removes generated artifacts when new requirements
// were written via the inline editor (always different content).
func (m *startupModel) cleanAfterEditorSave(newContent string) {
	ws := artifacts.Workspace{Dir: m.wsDir}

	oldContent, err := ws.ReadFile(artifacts.RequirementsFile)
	if err != nil {
		return
	}

	if strings.TrimSpace(string(oldContent)) == strings.TrimSpace(newContent) {
		return
	}

	ws.CleanGeneratedArtifacts()
}

func (m startupModel) View() string {
	switch m.phase {
	case startupPhaseSetup:
		return m.setup.View()
	case startupPhaseModulePath:
		return m.viewModulePath()
	case startupPhasePicker:
		return m.picker.View()
	case startupPhaseEditor:
		return m.editor.View()
	case startupPhaseProjectList:
		return m.projectPicker.View()
	case startupPhaseOpenProject:
		return m.dirBrowser.View()
	case startupPhaseHistory:
		return m.taskHistory.View()
	case startupPhaseModelMemory:
		return m.modelMemory.View()
	default:
		return m.home.View()
	}
}

// needsModulePath returns true if the project is Go, has no module path configured,
// and no go.mod exists in the project root.
func (m startupModel) needsModulePath() bool {
	return NeedsModulePath(m.root, m.cfg)
}

func (m *startupModel) showModulePathInput() tea.Cmd {
	ti := textinput.New()
	ti.Placeholder = "github.com/user/project"
	ti.CharLimit = 200
	ti.Width = m.width - 10

	if suggested := guessModulePath(m.root); suggested != "" {
		ti.SetValue(suggested)
	}

	m.moduleInput = ti
	m.phase = startupPhaseModulePath
	return m.moduleInput.Focus()
}

func (m *startupModel) showPicker() tea.Cmd {
	m.picker = NewPicker(m.root, m.wsReqPath)
	m.picker.SetSize(m.width, m.height)
	m.phase = startupPhasePicker
	return m.picker.Init()
}

// guessModulePath tries to derive a Go module path from the git remote origin URL.
// Supports both SSH (git@host:owner/repo.git) and HTTPS (https://host/owner/repo.git) formats.
func guessModulePath(root string) string {
	gc := gitclient.New(root)
	remote, err := gc.RemoteURL("origin")
	if err != nil || remote == "" {
		return ""
	}

	remote = strings.TrimSuffix(remote, ".git")

	// SSH format: git@github.com:owner/repo
	if strings.HasPrefix(remote, "git@") {
		remote = strings.TrimPrefix(remote, "git@")
		remote = strings.Replace(remote, ":", "/", 1)
		return remote
	}

	// HTTPS format: https://github.com/owner/repo
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(remote, scheme) {
			return strings.TrimPrefix(remote, scheme)
		}
	}

	return ""
}

// resumePendingAction continues the home action that was interrupted to ask for
// the Go module path. Falls back to returning home for any other action.
func (m *startupModel) resumePendingAction() tea.Cmd {
	switch m.pendingAction {
	case homeActionNewTask:
		cleanWorkspace(m.wsDir, false)
		m.chatMode = true
		return tea.Quit
	case homeActionRunPipeline:
		return m.showPicker()
	case homeActionResumeProject:
		m.reviewProject = true
		return tea.Quit
	default:
		return m.transitionToHome()
	}
}

// transitionToHome builds a fresh HomeModel for the current root/cfg and
// switches to the home phase. Used after project selection and module path input.
func (m *startupModel) transitionToHome() tea.Cmd {
	m.home = NewHomeModel(m.cfg, m.root)
	m.home.width, m.home.height = m.width, m.height
	m.home.syncViewport()
	m.phase = startupPhaseHome
	return m.home.Init()
}

// switchProject handles switching to a different project directory.
// It updates root, config, workspace paths, checks module path, and transitions to home.
func (m *startupModel) switchProject(projectPath string) tea.Cmd {
	if isHomeDir(projectPath) {
		// Never allow the home directory as a project root.
		m.phase = startupPhaseHome
		return m.home.Init()
	}

	m.root = projectPath
	_ = os.Chdir(projectPath)
	_ = SaveRecentProject(projectPath)

	newCfg, err := config.Load(projectPath)
	if err != nil {
		newCfg = m.cfg
	}
	m.cfg = newCfg
	m.wsDir = filepath.Join(projectPath, artifacts.DirName)
	m.wsReqPath = filepath.Join(projectPath, artifacts.DirName, artifacts.RequirementsFile)

	// Auto-detect module path from go.mod.
	if m.cfg.Project.ModulePath == "" {
		m.cfg.Project.ModulePath = detectGoModulePath(m.root)
	}

	// If this Go project needs a module path, ask before showing home.
	if m.needsModulePath() {
		return m.showModulePathInput()
	}

	return m.transitionToHome()
}

// detectGoModulePath reads the module path from an existing go.mod file.
func detectGoModulePath(root string) string {
	// Use DirFS to safely scope file access within root directory
	data, err := fs.ReadFile(os.DirFS(root), "go.mod")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

func (m startupModel) viewModulePath() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	exampleStyle := lipgloss.NewStyle().Foreground(crt.primary)
	hintStyle := lipgloss.NewStyle().Foreground(crt.success)
	sep := strings.Repeat("─", m.width)

	projectName := filepath.Base(m.root)

	lines := []string{
		titleStyle.Render("Go Module Path"),
		sep,
		"",
		"  No go.mod found. A module path is required for Go projects.",
		"  It will be used for imports between packages and to run go mod init.",
		"",
		dimStyle.Render("  Format: <host>/<owner>/<repo>"),
		"",
		exampleStyle.Render("  Examples:"),
		exampleStyle.Render("    github.com/yourname/" + projectName),
		exampleStyle.Render("    gitlab.com/team/" + projectName),
		exampleStyle.Render("    " + projectName + "  (for local-only projects)"),
	}

	if suggested := guessModulePath(m.root); suggested != "" {
		lines = append(lines,
			"",
			hintStyle.Render("  ✓ Detected from git remote: "+suggested),
		)
	}

	lines = append(lines,
		"",
		"  "+m.moduleInput.View(),
		"",
		sep,
		dimStyle.Render("Enter confirm   Esc back"),
	)

	return strings.Join(lines, "\n")
}

// cleanWorkspace removes all files in the workspace directory except preserved ones.
// When preserveRequirements is false, requirements.md is also removed.
// cleanWorkspace removes generated pipeline artifacts but always preserves
// project configuration and project memory. Memory (memory/ + memory.db*)
// is institutional knowledge and survives every reset.
func cleanWorkspace(wsDir string, preserveRequirements bool) {
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		return
	}
	preserve := map[string]bool{
		artifacts.ProjectConfigFile: true,
		artifacts.MemoryDirName:     true, // memory/ — Markdown facts/decisions
	}
	if preserveRequirements {
		preserve[artifacts.RequirementsFile] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if preserve[name] {
			continue
		}
		// Preserve the SQLite memory index and its WAL/SHM companions.
		if name == artifacts.MemoryDBFile || strings.HasPrefix(name, artifacts.MemoryDBFile+"-") {
			continue
		}
		_ = os.RemoveAll(filepath.Join(wsDir, name))
	}
}

// NeedsModulePath returns true if the project looks like Go, has no module path
// configured, and no go.mod exists in root.
func NeedsModulePath(root string, cfg config.Config) bool {
	lang := cfg.Project.Language
	if lang != "" && lang != "go" {
		return false
	}
	if cfg.Project.ModulePath != "" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err != nil
}

// detectDefaultRunnerModel finds the most common runner+model pair across all
// agents. After a save, all non-overridden agents share the same pair, so the
// majority vote reliably recovers the "default" that was chosen in Setup.
// Tie-breaking is deterministic (alphabetical by runner, then model).
func detectDefaultRunnerModel(agents map[string]config.AgentConfig) (string, string) {
	type pair struct{ runner, model string }
	counts := make(map[pair]int)
	for _, ac := range agents {
		counts[pair{ac.Runner, ac.Model}]++
	}

	var bestPair pair
	bestCount := 0
	for p, c := range counts {
		if c > bestCount || (c == bestCount && pairLess(p.runner, p.model, bestPair.runner, bestPair.model)) {
			bestCount = c
			bestPair = p
		}
	}

	return bestPair.runner, bestPair.model
}

// pairLess provides stable ordering for tie-breaking: alphabetical by runner, then model.
func pairLess(r1, m1, r2, m2 string) bool {
	if r1 != r2 {
		return r1 < r2
	}
	return m1 < m2
}

// StartupResult holds the outcome of the startup flow.
type StartupResult struct {
	ReqPath       string        // non-empty when legacy pipeline was selected
	ChatMode      bool          // true when PM chat-based requirements gathering was selected
	Resume        bool          // true when the user chose to resume an interrupted task
	ReviewProject bool          // true when the user chose "Resume Project" (PM reviews .orchestrator)
	Cfg           config.Config // possibly updated config
}

// RunStartup shows the home/picker/editor flow and returns the result.
// Empty ReqPath with ChatMode=false means the user quit without selecting.
func RunStartup(root, wsReqPath string, cfg config.Config) (StartupResult, error) {
	m := newStartupModel(root, wsReqPath, cfg)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	result, err := prog.Run()
	if err != nil {
		return StartupResult{Cfg: cfg}, err
	}
	final, ok := result.(startupModel)
	if !ok {
		return StartupResult{Cfg: cfg}, nil
	}
	return StartupResult{
		ReqPath:       final.reqPath,
		ChatMode:      final.chatMode,
		Resume:        final.resume,
		ReviewProject: final.reviewProject,
		Cfg:           final.cfg,
	}, nil
}

// modulePathModel is a minimal Bubble Tea model that only shows the module path
// input screen. Used when --requirements is provided but module path is missing.
type modulePathModel struct {
	moduleInput textinput.Model
	root        string
	wsDir       string
	modulePath  string // result
	cancelled   bool
	width       int
	height      int
}

func newModulePathModel(root, wsDir string) modulePathModel {
	ti := textinput.New()
	ti.Placeholder = "github.com/user/project"
	ti.CharLimit = 200
	ti.Width = 70

	if suggested := guessModulePath(root); suggested != "" {
		ti.SetValue(suggested)
	}

	ti.Focus()

	return modulePathModel{
		root:        root,
		wsDir:       wsDir,
		moduleInput: ti,
		width:       80,
		height:      24,
	}
}

func (m modulePathModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m modulePathModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wm.Width, wm.Height
		m.moduleInput.Width = wm.Width - 10
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			val := strings.TrimSpace(m.moduleInput.Value())
			if val != "" {
				m.modulePath = val
				projectCfg := config.LoadProject(m.root)
				projectCfg.Project.ModulePath = val
				_ = config.Save(m.root, projectCfg)
				return m, tea.Quit
			}
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.moduleInput, cmd = m.moduleInput.Update(msg)
	return m, cmd
}

func (m modulePathModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	exampleStyle := lipgloss.NewStyle().Foreground(crt.primary)
	hintStyle := lipgloss.NewStyle().Foreground(crt.success)
	sep := strings.Repeat("─", m.width)

	projectName := filepath.Base(m.root)

	lines := []string{
		titleStyle.Render("Go Module Path"),
		sep,
		"",
		"  No go.mod found. A module path is required for Go projects.",
		"  It will be used for imports between packages and to run go mod init.",
		"",
		dimStyle.Render("  Format: <host>/<owner>/<repo>"),
		"",
		exampleStyle.Render("  Examples:"),
		exampleStyle.Render("    github.com/yourname/" + projectName),
		exampleStyle.Render("    gitlab.com/team/" + projectName),
		exampleStyle.Render("    " + projectName + "  (for local-only projects)"),
	}

	if suggested := guessModulePath(m.root); suggested != "" {
		lines = append(lines,
			"",
			hintStyle.Render("  ✓ Detected from git remote: "+suggested),
		)
	}

	lines = append(lines,
		"",
		"  "+m.moduleInput.View(),
		"",
		sep,
		dimStyle.Render("Enter confirm   Esc cancel"),
	)

	return strings.Join(lines, "\n")
}

// RunModulePathPrompt shows just the module path input screen and returns
// the chosen path. Returns ("", nil) if the user cancelled.
func RunModulePathPrompt(root, wsDir string) (string, error) {
	m := newModulePathModel(root, wsDir)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	result, err := prog.Run()
	if err != nil {
		return "", err
	}
	final, ok := result.(modulePathModel)
	if !ok || final.cancelled {
		return "", nil
	}
	return final.modulePath, nil
}
