package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// applyProjectSetup persists per-agent overrides from a project-setup completion
// and returns the reloaded merged config. The bool is false when reload fails.
func applyProjectSetup(root string, msg setupDoneMsg) (config.Config, bool) {
	projectAgents := make(map[string]config.AgentConfig)
	for role, ov := range msg.agentOverrides {
		ac := config.AgentConfig{}
		if ov.runner != "" {
			ac.Runner = ov.runner
		}
		if ov.model != "" {
			ac.Model = ov.model
		}
		projectAgents[role] = ac
	}

	projectCfg := config.LoadProject(root)
	if len(projectAgents) > 0 {
		projectCfg.Agents = projectAgents
	} else {
		projectCfg.Agents = nil
	}
	if msg.progLanguage != "" {
		projectCfg.Project.Language = msg.progLanguage
	}
	projectCfg.Project.Name = msg.projectName
	_ = config.Save(root, projectCfg)

	reloaded, err := config.Load(root)
	if err != nil {
		return config.Config{}, false
	}
	return reloaded, true
}

// modelSetupModel is a standalone Bubble Tea model that shows only the per-agent
// project setup screen. It is used by the "Pause & change model" flow so the
// user can swap an agent's model mid-task without going through the home menu.
type modelSetupModel struct {
	setup  SetupModel
	root   string
	cfg    config.Config
	width  int
	height int
}

func newModelSetupModel(root string, cfg config.Config) modelSetupModel {
	currentRunner, currentModel := detectDefaultRunnerModel(cfg.Agents)
	projectCfg := config.LoadProject(root)
	projectOverrides := make(map[string]config.AgentConfig)
	if projectCfg.Agents != nil {
		projectOverrides = projectCfg.Agents
	}
	setup := NewSetupModelWithOverrides(currentRunner, currentModel,
		cfg.Project.Language, cfg.Project.Name, projectOverrides)
	setup.root = root
	return modelSetupModel{setup: setup, root: root, cfg: cfg, width: 80, height: 24}
}

func (m modelSetupModel) Init() tea.Cmd { return m.setup.Init() }

func (m modelSetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wm.Width, wm.Height
		m.setup.width, m.setup.height = wm.Width, wm.Height
		m.setup.syncViewport()
	}
	switch msg := msg.(type) {
	case setupDoneMsg:
		if reloaded, ok := applyProjectSetup(m.root, msg); ok {
			m.cfg = reloaded
		}
		return m, tea.Quit
	case setupCancelledMsg:
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.setup, cmd = m.setup.Update(msg)
	return m, cmd
}

func (m modelSetupModel) View() string { return m.setup.View() }

// RunModelSetup shows only the per-agent project setup screen and returns the
// (possibly updated) config. Used by the pause-to-change-model flow.
func RunModelSetup(root string, cfg config.Config) (config.Config, error) {
	m := newModelSetupModel(root, cfg)
	m.setup.syncViewport()
	prog := tea.NewProgram(m, tea.WithAltScreen())
	result, err := prog.Run()
	if err != nil {
		return cfg, err
	}
	if final, ok := result.(modelSetupModel); ok {
		return final.cfg, nil
	}
	return cfg, nil
}
