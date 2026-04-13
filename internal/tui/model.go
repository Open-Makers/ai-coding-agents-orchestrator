package tui

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/prompts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

var agentOrder = []bus.AgentRole{
	bus.RolePM,
	bus.RolePlanner,
	bus.RoleCoder,
	bus.RoleTester,
	bus.RoleReviewer,
	bus.RoleUXReviewer,
	bus.RoleSecurity,
	bus.RoleQA,
}

type overlayKind int

const (
	overlayNone   overlayKind = iota
	overlayPicker             // picker → optional editor
	overlayEditor
	overlayGit
	overlayChat
	overlayArtifact // auto-opened on human gate
)

// planningGates lists the artifact filenames gated during the planning phase,
// in the order they are presented to the user.
var planningGates = []string{
	"architecture.md",
	"implementation_plan.md",
	"prompts.md",
}

// gateLabel returns a short human-readable label for a planning gate artifact.
func gateLabel(filename string) string {
	switch filename {
	case "architecture.md":
		return "architecture"
	case "implementation_plan.md":
		return "plan"
	case "prompts.md":
		return "prompts"
	}
	return filename
}

// Model is the root Bubble Tea model for the orchestrator TUI.
type Model struct {
	panels        map[bus.AgentRole]AgentPanelModel
	activeRole    bus.AgentRole // agent currently shown in the main area
	phase         string        // current pipeline phase for the phase bar
	statusbar     StatusBarModel
	sysmon        SysmonModel                   // system monitor panel (right side)
	showSysmon    bool                          // toggle for sysmon visibility
	agentConfigs  map[string]config.AgentConfig // per-agent runner/model config
	events        <-chan bus.Message
	pipeline      *orchestrator.Pipeline
	cancelFunc    context.CancelFunc // cancels the pipeline context
	llm           runner.LLMRunner
	root          string
	wsPath        string
	width         int
	height        int
	quitting      bool
	confirmQuit   bool   // true when showing quit confirmation dialog
	cancelConfirm bool   // true when showing cancel confirmation dialog
	cancelled     bool   // true after user confirmed cancel — auto-return to menu
	gateMsg       string // non-empty while pipeline is waiting for human approval
	pipelineErr   string // non-empty when pipeline finished with an error
	pipelineDone  bool   // true after pipeline finished (enables return-to-menu)
	returnToMenu  bool   // true when user chose to return to menu
	log           *slog.Logger

	// Planning sub-stage tracking.
	gateArtifact  string          // filename currently awaiting approval
	approvedGates map[string]bool // filenames approved so far

	// Typed overlay state — only one active at a time.
	overlay         overlayKind
	overlayPicker   PickerModel
	overlayEditor   EditorModel
	overlayGit      GitPanelModel
	overlayChat     ChatModel
	overlayArtifact ArtifactViewerModel
}

// PipelineReadyWithCancelMsg returns a tea.Msg with pipeline and cancel func.
func PipelineReadyWithCancelMsg(p *orchestrator.Pipeline, cancel context.CancelFunc) tea.Msg {
	return pipelineReadyMsg{p: p, cancel: cancel}
}

// ReturnToMenu returns true if the user chose to go back to the main menu
// after pipeline completion (instead of quitting).
func (m Model) ReturnToMenu() bool {
	return m.returnToMenu
}

// New creates the root TUI model.
// pipeline may be nil initially; send PipelineReadyWithCancelMsg once it is ready.
// llm is used for the chat overlay (may be nil).
func New(events <-chan bus.Message, pipeline *orchestrator.Pipeline, root, wsPath string, llm runner.LLMRunner, cfg config.Config) Model {
	panels := make(map[bus.AgentRole]AgentPanelModel)
	projectLang := resolveLanguageFromRoot(root, cfg)
	for _, role := range agentOrder {
		p := NewAgentPanel(role)
		if projectLang != "" {
			p.SetLanguage(projectLang)
		}
		panels[role] = p
	}

	runnerName, modelName := runnerModelFromConfig(cfg)

	return Model{
		panels:        panels,
		activeRole:    bus.RolePM,
		phase:         "idle",
		statusbar:     NewStatusBar(80).WithBranch(GitBranch(root)).WithState("idle").WithRunnerModel(runnerName, modelName),
		sysmon:        NewSysmon(),
		showSysmon:    true,
		agentConfigs:  cfg.Agents,
		events:        events,
		pipeline:      pipeline,
		llm:           llm,
		root:          root,
		wsPath:        wsPath,
		width:         80,
		height:        24,
		log:           logging.ForComponent("tui_model"),
		approvedGates: make(map[string]bool),
	}
}

// runnerModelFromConfig extracts the runner name and model from the planner agent config.
func runnerModelFromConfig(cfg config.Config) (string, string) {
	if ac, ok := cfg.Agents["planner"]; ok {
		r := ac.Runner
		if r == "" {
			r = "opencode"
		}
		return r, ac.Model
	}
	return "opencode", ""
}

// runnerModelForRole returns the runner and model configured for a given agent role.
// Falls back to the global default (most common pair) when the role has no explicit config.
func runnerModelForRole(agents map[string]config.AgentConfig, role bus.AgentRole) (string, string) {
	defaultRunner, defaultModel := detectDefaultRunnerModel(agents)

	ac, ok := agents[string(role)]
	if !ok {
		return defaultRunner, defaultModel
	}

	r := ac.Runner
	mdl := ac.Model
	if r == "" {
		r = defaultRunner
	}
	if mdl == "" {
		mdl = defaultModel
	}
	return r, mdl
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitForBusEvent(m.events), m.sysmon.Init()}
	for _, p := range m.panels {
		cmds = append(cmds, p.Init())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Layout on resize — always, even with overlay active.
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wm.Width
		m.height = wm.Height
		m.layout()
	}

	// BusMessageMsg is always processed regardless of overlay state.
	// This prevents the listener chain from breaking while an overlay is open.
	if busMsg, ok := msg.(BusMessageMsg); ok {
		bm := busMsg.Msg
		m.log.Debug("bus message received",
			slog.String("type", string(bm.Type)),
			slog.String("from", string(bm.From)),
			slog.String("to", string(bm.To)),
		)
		switch bm.Type {
		case bus.MsgHumanGate:
			if s, ok := bm.Payload.(string); ok {
				m.gateMsg = s
				m.gateArtifact = s
				m.log.Info("human gate activated",
					slog.String("artifact", s),
					slog.String("label", gateLabel(s)),
				)
				var reviseFn func(string, string) error
				if m.pipeline != nil {
					pipeline := m.pipeline
					reviseFn = func(artifact, feedback string) error {
						return pipeline.ReviseArtifact(context.Background(), artifact, feedback)
					}
				}
				m.overlayArtifact = newArtifactViewer(m.wsPath, s, m.contentWidth(), m.height-2, reviseFn)
				m.overlay = overlayArtifact
				m.statusbar = m.statusbar.WithState("⏸ " + gateLabel(s) + " — waiting for approval")
			}
		case bus.MsgEvent:
			if s, ok := bm.Payload.(string); ok {
				// Detect stage transitions (e.g. "── Stage 2/5: Must Have — Auth ──").
				if stageInfo := extractStageInfo(s); stageInfo != "" {
					prevStage := m.statusbar.stageInfo
					m.statusbar = m.statusbar.WithStageInfo(stageInfo)
					if prevStage == "" {
						cmds = append(cmds, statusBarTick())
					}
				}
				// Clear stage info only when pipeline is fully done.
				if s == "done" {
					m.statusbar = m.statusbar.WithStageInfo("")
				}
				// Switch active panel based on pipeline state changes.
				if role := stateToRole(s); role != "" {
					m.activeRole = role
					r, mdl := runnerModelForRole(m.agentConfigs, role)
					m.statusbar = m.statusbar.WithRunnerModel(r, mdl)
				}
				for _, ps := range []string{"pm", "planning", "coding", "fixing", "testing", "reviewing", "ux_reviewing", "security", "qa", "done"} {
					if s == ps {
						m.phase = s
						m.statusbar = m.statusbar.WithState(s)
						// Start elapsed timer on first coding handoff.
						if s == "coding" && m.pipeline != nil && m.statusbar.codingStarted.IsZero() {
							codingStart := m.pipeline.CodingStarted()
							if !codingStart.IsZero() {
								m.statusbar = m.statusbar.WithCodingStarted(codingStart)
							}
						}
					}
				}
			}
			// Count output tokens for the status bar.
			if tp, ok := bm.Payload.(bus.TokenPayload); ok && !tp.Done {
				m.statusbar = m.statusbar.AddTokenChars(len(tp.Text))
			}
		case bus.MsgRequest:
			if bm.To != "" && bm.To != bus.RoleSystem {
				m.activeRole = bm.To
				r, mdl := runnerModelForRole(m.agentConfigs, bm.To)
				m.statusbar = m.statusbar.WithRunnerModel(r, mdl)
			}
		case bus.MsgResponse:
			m.gateMsg = ""
		}

		agentMsg := busToAgentEvent(bm)
		if agentMsg.Role != "" {
			if p, ok := m.panels[agentMsg.Role]; ok {
				updated, cmd := p.Update(agentMsg)
				m.panels[agentMsg.Role] = updated
				cmds = append(cmds, cmd)
			}
		}
		cmds = append(cmds, waitForBusEvent(m.events))
		return m, tea.Batch(cmds...)
	}

	// Sysmon ticks are processed independently — always, regardless of overlay.
	if tickMsg, ok := msg.(sysmonTickMsg); ok {
		var cmd tea.Cmd
		m.sysmon, cmd = m.sysmon.Update(tickMsg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
	}

	// Spinner ticks are also processed globally for agent panels.
	if _, ok := msg.(spinner.TickMsg); ok {
		for role, p := range m.panels {
			updated, cmd := p.Update(msg)
			m.panels[role] = updated
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Ctrl+T toggles sysmon from any context (including overlays).
	if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+t" {
		m.showSysmon = !m.showSysmon
		m.layout()
		// Resize the active overlay to fit new content width.
		return m, m.resizeActiveOverlay()
	}

	// Non-bus messages: route to overlay or normal handling.
	if m.overlay != overlayNone {
		return m.updateOverlay(msg)
	}

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()

	case tea.KeyMsg:
		// Quit confirmation intercepts all keys.
		if m.confirmQuit {
			switch msg.String() {
			case "y", "Y", "enter":
				m.quitting = true
				if m.cancelFunc != nil {
					m.cancelFunc()
				}
				return m, tea.Quit
			default:
				m.confirmQuit = false
			}
			return m, nil
		}

		// Cancel confirmation intercepts all keys.
		if m.cancelConfirm {
			switch msg.String() {
			case "y", "Y", "enter":
				if m.cancelFunc != nil {
					m.cancelFunc()
				}
				m.cancelConfirm = false
				m.cancelled = true
				m.statusbar = m.statusbar.WithState("cancelling…")
			default:
				m.cancelConfirm = false
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "Q":
			m.confirmQuit = true
		case "m", "M":
			if m.pipelineDone {
				m.returnToMenu = true
				return m, tea.Quit
			}
		case "ctrl+x":
			if m.pipelineDone {
				// Pipeline already finished (error or success) — return to menu.
				m.returnToMenu = true
				return m, tea.Quit
			}
			m.cancelConfirm = true
		case "ctrl+a":
			if m.pipeline != nil && m.gateArtifact != "" {
				m.approvedGates[m.gateArtifact] = true
				m.log.Info("gate approved (shortcut)",
					slog.String("artifact", m.gateArtifact),
				)
				m.pipeline.Approve()
				m.statusbar = m.statusbar.WithState("✓ " + gateLabel(m.gateArtifact) + " approved")
				m.gateMsg = ""
				m.gateArtifact = ""
				m.overlay = overlayNone
			}
		case "ctrl+r":
			picker := NewPicker(m.root, m.wsPath)
			picker.SetSize(m.contentWidth(), m.height)
			m.overlayPicker = picker
			m.overlay = overlayPicker
		case "ctrl+g":
			gp, err := NewGitPanel(m.root)
			if err == nil {
				gp.SetSize(m.contentWidth(), m.height)
				m.overlayGit = gp
				m.overlay = overlayGit
			}
		case "ctrl+c":
			if m.llm != nil {
				systemPrompt := m.buildPMChatPrompt()
				chat := NewChat(m.llm, systemPrompt)
				if m.pipeline != nil {
					pipeline := m.pipeline
					chat = chat.WithReviseFn(func(artifact, feedback string) error {
						return pipeline.ReviseArtifact(context.Background(), artifact, feedback)
					})
				}
				chat.SetSize(m.contentWidth(), m.height)
				m.overlayChat = chat
				m.overlay = overlayChat
			}
		default:
			// Forward scroll keys to the active agent panel.
			if p, ok := m.panels[m.activeRole]; ok {
				updated, cmd := p.Update(msg)
				m.panels[m.activeRole] = updated
				cmds = append(cmds, cmd)
			}
		}

	case pipelineReadyMsg:
		m.pipeline = msg.p
		if msg.cancel != nil {
			m.cancelFunc = msg.cancel
		}

	case PipelineDoneMsg:
		m.pipelineDone = true
		if m.cancelled {
			m.returnToMenu = true
			return m, tea.Quit
		}
		if msg.Err != nil {
			m.pipelineErr = msg.Err.Error()
			m.log.Error("pipeline finished with error", slog.String("error", m.pipelineErr))
			m.statusbar = m.statusbar.WithState("✗ error — m/ctrl+x menu  q quit")
		} else {
			m.log.Info("pipeline completed successfully")
			m.statusbar = m.statusbar.WithState("✓ done — m/ctrl+x menu  q quit")
		}

	case statusBarTickMsg:
		if m.statusbar.stageInfo != "" {
			m.statusbar = m.statusbar.AdvanceScroll()
			cmds = append(cmds, statusBarTick())
		}
	}

	return m, tea.Batch(cmds...)
}

// updateOverlay dispatches to the currently active overlay.
func (m Model) updateOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayNone:
		// No overlay active — nothing to dispatch.
	case overlayPicker:
		switch msg := msg.(type) {
		case PickerSelectedMsg:
			if msg.IsNew {
				ed := NewEditor("")
				m.overlayEditor = ed
				m.overlay = overlayEditor
				return m, nil
			}
			// Selected existing file — close picker.
			m.overlay = overlayNone
			return m, nil
		default:
			var cmd tea.Cmd
			m.overlayPicker, cmd = m.overlayPicker.Update(msg)
			return m, cmd
		}

	case overlayEditor:
		switch msg.(type) {
		case EditorCancelledMsg:
			m.overlay = overlayNone
		case RequirementsSavedMsg:
			m.overlay = overlayNone
		default:
			var cmd tea.Cmd
			m.overlayEditor, cmd = m.overlayEditor.Update(msg)
			return m, cmd
		}

	case overlayGit:
		switch msg.(type) {
		case GitPanelClosedMsg:
			m.overlay = overlayNone
		default:
			var cmd tea.Cmd
			m.overlayGit, cmd = m.overlayGit.Update(msg)
			return m, cmd
		}

	case overlayChat:
		switch msg.(type) {
		case ChatClosedMsg:
			m.overlay = overlayNone
		default:
			var cmd tea.Cmd
			m.overlayChat, cmd = m.overlayChat.Update(msg)
			return m, cmd
		}

	case overlayArtifact:
		switch msg := msg.(type) {
		case artifactViewerClosedMsg:
			m.overlay = overlayNone
			if msg.regenerate && m.pipeline != nil {
				m.log.Info("regeneration requested",
					slog.String("artifact", m.gateArtifact),
				)
				m.pipeline.Regenerate()
				m.gateMsg = ""
				m.gateArtifact = ""
				m.statusbar = m.statusbar.WithState("regenerating…")
			} else if msg.approved && m.pipeline != nil {
				approved := m.gateArtifact
				m.approvedGates[approved] = true
				m.log.Info("gate approved",
					slog.String("artifact", approved),
					slog.String("label", gateLabel(approved)),
				)
				m.pipeline.Approve()
				m.gateMsg = ""
				m.gateArtifact = ""
				m.statusbar = m.statusbar.WithState("✓ " + gateLabel(approved) + " approved")
			} else {
				m.log.Debug("gate dismissed without approval",
					slog.String("artifact", m.gateArtifact),
				)
			}
		case tea.WindowSizeMsg:
			// Shrink height for phase bar + status bar below the overlay.
			// Shrink width for sysmon panel on the right.
			msg.Height -= 2
			msg.Width = m.contentWidth()
			var cmd tea.Cmd
			m.overlayArtifact, cmd = m.overlayArtifact.Update(msg)
			return m, cmd
		default:
			var cmd tea.Cmd
			m.overlayArtifact, cmd = m.overlayArtifact.Update(msg)
			return m, cmd
		}

	default:
		// overlayNone is handled before this method is called.
	}

	// Overlay closed — resume listening to bus events.
	return m, waitForBusEvent(m.events)
}

func (m Model) View() string {
	if m.quitting {
		return "bye\n"
	}

	switch m.overlay {
	case overlayNone:
		// Fall through to main view below.
	case overlayPicker:
		return m.withSysmon(m.overlayPicker.View())
	case overlayEditor:
		return m.withSysmon(m.overlayEditor.View())
	case overlayGit:
		return m.withSysmon(m.overlayGit.View())
	case overlayChat:
		return m.withSysmon(m.overlayChat.View())
	case overlayArtifact:
		artifactView := strings.Join([]string{
			strings.TrimRight(m.overlayArtifact.View(), "\n"),
			m.renderPhaseBar(),
			m.statusbar.View(),
		}, "\n")
		return m.withSysmon(artifactView)
	}

	// Main area: single active agent panel (or congratulations on completion).
	panelH := m.height - 3 // phase bar (1) + status bar (1) + newline
	if panelH < 4 {
		panelH = 4
	}

	// Determine sysmon width and agent panel width.
	sysmonW := 0
	agentW := m.width
	if m.showSysmon && m.width >= 100 {
		sysmonW = 38
		if m.width >= 140 {
			sysmonW = 44
		}
		if m.width >= 180 {
			sysmonW = 50
		}
		agentW = m.width - sysmonW - 1 // 1 for gap
	}

	var parts []string

	if m.pipelineDone && m.pipelineErr == "" {
		agentView := m.renderCongratulations(panelH)
		if sysmonW > 0 {
			m.sysmon.SetSize(sysmonW, panelH)
			sysmonView := m.sysmon.View()
			// Place congratulations in left, sysmon in right.
			joined := lipgloss.JoinHorizontal(lipgloss.Top,
				lipgloss.NewStyle().Width(agentW).Render(agentView),
				" ",
				sysmonView,
			)
			parts = append(parts, joined)
		} else {
			parts = append(parts, agentView)
		}
	} else {
		p := m.panels[m.activeRole]
		p.SetSize(agentW, panelH)
		agentView := p.View()

		if sysmonW > 0 {
			m.sysmon.SetSize(sysmonW, panelH)
			sysmonView := m.sysmon.View()
			joined := lipgloss.JoinHorizontal(lipgloss.Top, agentView, " ", sysmonView)
			parts = append(parts, joined)
		} else {
			parts = append(parts, agentView)
		}
	}

	if m.pipelineErr != "" {
		banner := lipgloss.NewStyle().
			Background(crt.border).
			Foreground(crt.warn).
			Bold(true).
			Padding(0, 2).
			Width(m.width - 4).
			Render("✗  " + m.pipelineErr)
		parts = append(parts, banner)
	}
	parts = append(parts, m.renderPhaseBar(), m.statusbar.View())

	mainView := strings.Join(parts, "\n")

	if m.confirmQuit {
		dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
		warnStyle := lipgloss.NewStyle().Foreground(crt.warn).Bold(true)
		brightStyle := lipgloss.NewStyle().Foreground(crt.bright).Bold(true)
		confirmBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(crt.warn).
			Padding(1, 3).
			Render(
				warnStyle.Render("  QUIT ORCHESTRATOR?") + "\n\n" +
					dimStyle.Render("  Press ") +
					brightStyle.Render("y") +
					dimStyle.Render(" or ") +
					brightStyle.Render("Enter") +
					dimStyle.Render(" to confirm, any other key to cancel"),
			)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, confirmBox)
	}

	if m.cancelConfirm {
		dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
		warnStyle := lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
		brightStyle := lipgloss.NewStyle().Foreground(crt.bright).Bold(true)
		confirmBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(crt.primary).
			Padding(1, 3).
			Render(
				warnStyle.Render("  CANCEL PIPELINE AND RETURN TO MENU?") + "\n\n" +
					dimStyle.Render("  Press ") +
					brightStyle.Render("y") +
					dimStyle.Render(" or ") +
					brightStyle.Render("Enter") +
					dimStyle.Render(" to confirm, any other key to continue"),
			)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, confirmBox)
	}

	return mainView
}

// renderPhaseBar renders a one-line pipeline progress indicator.
// During the planning phase it expands into sub-stages:
//
//	✓ architecture → ◉ plan → ○ prompts → ○ coding → …
func (m Model) renderPhaseBar() string {
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	sepStyle := lipgloss.NewStyle().Foreground(crt.muted)
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#73daca")).Bold(true)

	phaseStyle := func(label string) lipgloss.Style {
		if c, ok := pipelineColors[label]; ok {
			return lipgloss.NewStyle().Foreground(c).Bold(true)
		}
		return lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
	}

	var parts []string

	// PM phase (before planning gates).
	pmPhase := "pm"
	if strings.Contains(m.phase, pmPhase) {
		parts = append(parts, phaseStyle("PM").Render("◉ PM"))
	} else if m.approvedGates[planningGates[0]] || strings.Contains(m.phase, "planning") || strings.Contains(m.phase, "coding") {
		parts = append(parts, doneStyle.Render("✓ PM"))
	} else {
		parts = append(parts, dimStyle.Render("○ PM"))
	}

	// Planning sub-stages: architecture → plan → prompts.
	for _, gate := range planningGates {
		label := strings.ToUpper(gateLabel(gate))
		activeStyle := phaseStyle(label)
		switch {
		case m.approvedGates[gate]:
			parts = append(parts, doneStyle.Render("✓ "+label))
		case m.gateArtifact == gate:
			parts = append(parts, activeStyle.Render("⏸ "+label))
		case strings.Contains(m.phase, "planning") && !m.approvedGates[gate] && m.gateArtifact == "":
			parts = append(parts, activeStyle.Render("◉ "+label))
		default:
			parts = append(parts, dimStyle.Render("○ "+label))
		}
	}

	// Post-planning phases.
	postPhases := []string{"coding", "testing", "reviewing", "ux_reviewing", "security", "qa", "done"}
	for _, ph := range postPhases {
		label := strings.ToUpper(ph)
		if strings.Contains(m.phase, ph) {
			parts = append(parts, phaseStyle(label).Render("◉ "+label))
		} else if ph == "coding" && m.phase == "fixing" {
			// Show FIXING indicator instead of coding dot.
			parts = append(parts, phaseStyle("FIXING").Render("⟳ FIXING"))
		} else {
			parts = append(parts, dimStyle.Render("○ "+label))
		}
	}

	return "  " + strings.Join(parts, sepStyle.Render(" → "))
}

// renderCongratulations renders a centered congratulations banner with pipeline summary.
func (m Model) renderCongratulations(height int) string {
	successColor := lipgloss.Color("#73daca")
	titleStyle := lipgloss.NewStyle().
		Foreground(successColor).
		Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	accentStyle := lipgloss.NewStyle().Foreground(crt.primary)
	headerStyle := lipgloss.NewStyle().Foreground(crt.bright).Bold(true)
	passedStyle := lipgloss.NewStyle().Foreground(successColor)
	skippedStyle := lipgloss.NewStyle().Foreground(crt.dim).Italic(true)
	warnStyle := lipgloss.NewStyle().Foreground(crt.warn)
	sectionStyle := lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
	bulletStyle := lipgloss.NewStyle().Foreground(crt.dim)
	labelStyle := lipgloss.NewStyle().Foreground(crt.dim)
	valueStyle := lipgloss.NewStyle().Foreground(crt.bright)
	totalStyle := lipgloss.NewStyle().Foreground(crt.primary).Bold(true)
	separatorStyle := lipgloss.NewStyle().Foreground(crt.border)

	var content strings.Builder
	content.WriteString(titleStyle.Render("🎉  Congratulations!"))
	content.WriteString("\n\n")
	content.WriteString(accentStyle.Render("Pipeline completed successfully."))
	content.WriteString("\n")

	// Read summary from artifacts if available.
	// Use DirFS to safely scope file access within workspace directory
	wsFS := os.DirFS(m.wsPath)
	if data, err := fs.ReadFile(wsFS, artifacts.SummaryFile); err == nil {
		summary := strings.TrimSpace(string(data))
		if summary != "" {
			content.WriteString("\n")
			for _, line := range strings.Split(summary, "\n") {
				trimmed := strings.TrimSpace(line)
				styled := m.styleSummaryLine(
					line, trimmed,
					headerStyle, separatorStyle, passedStyle,
					skippedStyle, warnStyle, sectionStyle,
					bulletStyle, labelStyle, valueStyle,
					totalStyle, dimStyle,
				)
				content.WriteString(styled)
				content.WriteString("\n")
			}
		}
	}

	content.WriteString("\n")
	content.WriteString(dimStyle.Render("Press ") +
		accentStyle.Render("m") +
		dimStyle.Render(" for menu  or  ") +
		accentStyle.Render("q") +
		dimStyle.Render(" to quit"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(successColor).
		Padding(1, 4).
		Render(content.String())

	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}

// styleSummaryLine applies context-aware styling to a single summary line.
func (m Model) styleSummaryLine(
	line, trimmed string,
	headerStyle, separatorStyle, passedStyle,
	skippedStyle, warnStyle, sectionStyle,
	bulletStyle, labelStyle, valueStyle,
	totalStyle, dimStyle lipgloss.Style,
) string {
	switch {
	case strings.HasPrefix(trimmed, "════"):
		return separatorStyle.Render(line)
	case strings.HasPrefix(trimmed, "PIPELINE COMPLETE"):
		return headerStyle.Render(line)
	case strings.HasPrefix(trimmed, "✓"):
		return passedStyle.Render(line)
	case strings.HasPrefix(trimmed, "○"):
		return skippedStyle.Render(line)
	case strings.HasPrefix(trimmed, "?"):
		return warnStyle.Render(line)
	case strings.HasPrefix(trimmed, "📋"):
		return sectionStyle.Render(line)
	case strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "•"):
		return sectionStyle.Render(line)
	case strings.HasPrefix(trimmed, "•"):
		return bulletStyle.Render(line)
	case strings.HasPrefix(trimmed, "TOTAL") || strings.HasPrefix(trimmed, "WALL CLOCK"):
		return m.styleDurationLine(line, totalStyle, totalStyle)
	case m.isDurationLine(trimmed):
		return m.styleDurationLine(line, labelStyle, valueStyle)
	case trimmed == "":
		return ""
	default:
		return dimStyle.Render(line)
	}
}

// isDurationLine checks if a line looks like an agent duration entry (e.g. "    pm           27s").
func (m Model) isDurationLine(trimmed string) bool {
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return false
	}
	last := fields[len(fields)-1]
	return strings.HasSuffix(last, "s") || strings.HasSuffix(last, "m") || strings.Contains(last, "m ")
}

// styleDurationLine styles a duration line with separate label and value colors.
func (m Model) styleDurationLine(line string, lStyle, vStyle lipgloss.Style) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return lStyle.Render(line)
	}
	// Find where the value starts — last field(s) that look like a duration.
	label := fields[0]
	value := strings.Join(fields[1:], " ")

	// Preserve original indentation.
	indent := ""
	for _, ch := range line {
		if ch == ' ' {
			indent += " "
		} else {
			break
		}
	}
	return indent + lStyle.Render(fmt.Sprintf("%-12s", label)) + " " + vStyle.Render(value)
}

// withSysmon composites the sysmon panel to the right of the given view
// when sysmon is enabled and the terminal is wide enough. The view is
// expected to already be rendered at contentWidth().
func (m Model) withSysmon(view string) string {
	sysmonW := m.sysmonWidth()
	if sysmonW == 0 {
		return view
	}
	cw := m.contentWidth()
	if cw < 20 {
		return view
	}
	m.sysmon.SetSize(sysmonW, m.height)
	sysmonView := m.sysmon.View()
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(cw).Render(view),
		" ",
		sysmonView,
	)
}

// sysmonWidth returns the current sysmon panel width, or 0 if hidden.
func (m Model) sysmonWidth() int {
	if !m.showSysmon || m.width < 100 {
		return 0
	}
	if m.width >= 180 {
		return 50
	}
	if m.width >= 140 {
		return 44
	}
	return 38
}

// contentWidth returns the available width for main content, subtracting
// sysmon panel width when visible. Overlays and editors should use this
// instead of m.width so they coexist with the monitor.
func (m Model) contentWidth() int {
	sw := m.sysmonWidth()
	if sw == 0 {
		return m.width
	}
	return m.width - sw - 1
}

// resizeActiveOverlay sends a synthetic WindowSizeMsg to the current overlay
// so it adapts to the new content width (e.g. after sysmon toggle).
func (m *Model) resizeActiveOverlay() tea.Cmd {
	cw := m.contentWidth()
	switch m.overlay {
	case overlayNone:
		// Nothing to resize.
	case overlayPicker:
		m.overlayPicker.SetSize(cw, m.height)
	case overlayEditor:
		m.overlayEditor, _ = m.overlayEditor.Update(tea.WindowSizeMsg{Width: cw, Height: m.height})
	case overlayGit:
		m.overlayGit.SetSize(cw, m.height)
	case overlayChat:
		m.overlayChat.SetSize(cw, m.height)
	case overlayArtifact:
		m.overlayArtifact, _ = m.overlayArtifact.Update(tea.WindowSizeMsg{Width: cw, Height: m.height - 2})
	}
	return nil
}

func (m *Model) layout() {
	panelH := m.height - 3
	if panelH < 4 {
		panelH = 4
	}

	agentW := m.width
	if m.showSysmon && m.width >= 100 {
		sysmonW := 38
		if m.width >= 140 {
			sysmonW = 44
		}
		if m.width >= 180 {
			sysmonW = 50
		}
		agentW = m.width - sysmonW - 1
		m.sysmon.SetSize(sysmonW, panelH)
	}

	for role, p := range m.panels {
		p.SetSize(agentW, panelH)
		m.panels[role] = p
	}
	m.statusbar = m.statusbar.WithWidth(m.width)
}

// buildPMChatPrompt constructs a system prompt for PM chat with current project context.
func (m *Model) buildPMChatPrompt() string {
	ws := artifacts.Workspace{Dir: m.wsPath}

	readArtifact := func(name string) string {
		data, err := ws.ReadFile(name)
		if err != nil {
			return "(not yet generated)"
		}
		return string(data)
	}

	requirements := readArtifact(artifacts.RequirementsFile)
	vision := readArtifact(artifacts.VisionFile)
	moscow := readArtifact(artifacts.MoscowFile)
	architecture := readArtifact(artifacts.ArchitectureFile)
	plan := readArtifact(artifacts.ImplementationPlanFile)
	stagePrompts := readArtifact(artifacts.PromptsFile)

	pipelineCtx := ""
	if m.phase != "" {
		pipelineCtx = fmt.Sprintf("Current pipeline phase: %s", m.phase)
	}

	return fmt.Sprintf(
		prompts.MustLoad("pm-chat"),
		requirements, vision, moscow, architecture, plan, stagePrompts, pipelineCtx,
	)
}

// extractStageInfo detects stage event messages and returns the stage label.
// Matches patterns like "── Stage 2/5: Must Have — Auth ──".
func extractStageInfo(event string) string {
	trimmed := strings.TrimLeft(event, "─ \t")
	trimmed = strings.TrimRight(trimmed, "─ \t")
	if strings.HasPrefix(trimmed, "Stage ") {
		return trimmed
	}
	return ""
}

// stateToRole maps a pipeline state string (from setState) to an agent role
// so the TUI can automatically switch the active panel.
func stateToRole(state string) bus.AgentRole {
	switch state {
	case "pm":
		return bus.RolePM
	case "planning":
		return bus.RolePlanner
	case "coding", "fixing":
		return bus.RoleCoder
	case "testing":
		return bus.RoleTester
	case "reviewing":
		return bus.RoleReviewer
	case "ux_reviewing":
		return bus.RoleUXReviewer
	case "security":
		return bus.RoleSecurity
	case "qa":
		return bus.RoleQA
	}
	return ""
}

// busToAgentEvent converts a bus.Message to an AgentEventMsg if applicable.
func busToAgentEvent(msg bus.Message) AgentEventMsg {
	role := msg.From
	if role == bus.RoleSystem {
		role = msg.To
	}

	var state AgentState
	var text string

	switch msg.Type {
	case bus.MsgRequest:
		if msg.To != "" && msg.To != bus.RoleSystem {
			state = AgentRunning
		}
	case bus.MsgResponse:
		state = AgentDone
	case bus.MsgHumanGate:
		state = AgentGate
	case bus.MsgEvent:
		switch p := msg.Payload.(type) {
		case bus.TokenPayload:
			if !p.Done {
				text = p.Text
			}
		case string:
			if p == "fixing" {
				state = AgentFixing
			} else if p == "done" {
				state = AgentDone
			} else if strings.Contains(p, "error") {
				state = AgentError
			}
		}
	}

	return AgentEventMsg{Role: role, Text: text, State: state}
}
