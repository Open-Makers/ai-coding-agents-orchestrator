package tui

import (
	"context"
	"log/slog"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// ControlModel renders the local control surface for tmux-backed runs.
// It keeps conversation, agent status summary, approval flow, and shortcuts
// in the launching terminal while agent output lives in external tmux panes.
type ControlModel struct {
	conv         ConversationModel
	statusbar    StatusBarModel
	agentConfigs map[string]config.AgentConfig // per-agent runner/model config
	events       <-chan bus.Message
	pipeline     *orchestrator.Pipeline
	llm          runner.LLMRunner
	root         string
	wsPath       string
	width        int
	height       int
	quitting     bool
	confirmQuit  bool
	gateMsg      string
	phase        string
	agentStates  map[bus.AgentRole]AgentState
	log          *slog.Logger

	// Planning sub-stage tracking.
	gateArtifact  string
	approvedGates map[string]bool

	overlay         overlayKind
	overlayPicker   PickerModel
	overlayEditor   EditorModel
	overlayGit      GitPanelModel
	overlayChat     ChatModel
	overlayArtifact ArtifactViewerModel
}

func NewControl(events <-chan bus.Message, pipeline *orchestrator.Pipeline, root, wsPath string, llm runner.LLMRunner, cfg config.Config) ControlModel {
	runnerName, modelName := runnerModelFromConfig(cfg)
	return ControlModel{
		conv:          NewConversation(80, 20),
		statusbar:     NewStatusBar(80).WithBranch(GitBranch(root)).WithState("idle").WithRunnerModel(runnerName, modelName),
		agentConfigs:  cfg.Agents,
		events:        events,
		pipeline:      pipeline,
		llm:           llm,
		root:          root,
		wsPath:        wsPath,
		width:         80,
		height:        24,
		phase:         "idle",
		log:           logging.ForComponent("tui_control"),
		approvedGates: make(map[string]bool),
		agentStates: map[bus.AgentRole]AgentState{
			bus.RolePlanner:  AgentWaiting,
			bus.RoleCoder:    AgentWaiting,
			bus.RoleTester:   AgentWaiting,
			bus.RoleReviewer: AgentWaiting,
		},
	}
}

func (m ControlModel) Init() tea.Cmd {
	return waitForBusEvent(m.events)
}

func (m ControlModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wm.Width
		m.height = wm.Height
		m.layout()
	}

	// BusMessageMsg is always processed regardless of overlay state.
	if busMsg, ok := msg.(BusMessageMsg); ok {
		bm := busMsg.Msg
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
				m.overlayArtifact = newArtifactViewer(m.wsPath, s, m.width, m.height-2, reviseFn)
				m.overlay = overlayArtifact
				m.statusbar = m.statusbar.WithState("⏸ " + gateLabel(s) + " — waiting for approval")
			}
		case bus.MsgEvent:
			if s, ok := bm.Payload.(string); ok {
				for _, ps := range []string{"planning", "coding", "testing", "reviewing", "fixing", "done"} {
					if strings.Contains(s, ps) {
						m.phase = s
						m.statusbar = m.statusbar.WithState(s)
					}
				}
			}
		case bus.MsgResponse:
			m.gateMsg = ""
		case bus.MsgRequest:
			if bm.To != "" && bm.To != bus.RoleSystem {
				r, mdl := runnerModelForRole(m.agentConfigs, bm.To)
				m.statusbar = m.statusbar.WithRunnerModel(r, mdl)
			}
		}

		agentMsg := busToAgentEvent(bm)
		if agentMsg.Role != "" && agentMsg.State != "" {
			m.agentStates[agentMsg.Role] = agentMsg.State
		}

		var convCmd tea.Cmd
		m.conv, convCmd = m.conv.Update(busMsg)
		return m, tea.Batch(convCmd, waitForBusEvent(m.events))
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
		switch msg.String() {
		case "q", "Q":
			m.quitting = true
			return m, tea.Quit
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
			picker.SetSize(m.width, m.height)
			m.overlayPicker = picker
			m.overlay = overlayPicker
		case "ctrl+g":
			gp, err := NewGitPanel(m.root)
			if err == nil {
				gp.SetSize(m.width, m.height)
				m.overlayGit = gp
				m.overlay = overlayGit
			}
		case "ctrl+c":
			if m.llm != nil {
				chat := NewChat(m.llm, "")
				chat.SetSize(m.width, m.height)
				m.overlayChat = chat
				m.overlay = overlayChat
			}
		}

	case pipelineReadyMsg:
		m.pipeline = msg.p
	}

	return m, nil
}

func (m ControlModel) updateOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayPicker:
		switch msg := msg.(type) {
		case PickerSelectedMsg:
			if msg.IsNew {
				m.overlayEditor = NewEditor("")
				m.overlay = overlayEditor
				return m, nil
			}
			m.overlay = overlayNone
			return m, nil
		default:
			var cmd tea.Cmd
			m.overlayPicker, cmd = m.overlayPicker.Update(msg)
			return m, cmd
		}
	case overlayEditor:
		switch msg.(type) {
		case EditorCancelledMsg, RequirementsSavedMsg:
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
			if msg.approved && m.pipeline != nil {
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
			}
		case tea.WindowSizeMsg:
			msg.Height -= 2
			var cmd tea.Cmd
			m.overlayArtifact, cmd = m.overlayArtifact.Update(msg)
			return m, cmd
		default:
			var cmd tea.Cmd
			m.overlayArtifact, cmd = m.overlayArtifact.Update(msg)
			return m, cmd
		}
	}

	// Overlay closed — resume listening to bus events.
	return m, waitForBusEvent(m.events)
}

func (m ControlModel) View() string {
	if m.quitting {
		return "bye\n"
	}

	switch m.overlay {
	case overlayPicker:
		return m.overlayPicker.View()
	case overlayEditor:
		return m.overlayEditor.View()
	case overlayGit:
		return m.overlayGit.View()
	case overlayChat:
		return m.overlayChat.View()
	case overlayArtifact:
		return strings.Join([]string{
			strings.TrimRight(m.overlayArtifact.View(), "\n"),
			m.renderPhaseBar(),
			m.statusbar.View(),
		}, "\n")
	}

	convLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render("  Conversation")

	parts := []string{
		m.renderAgentSummary(),
		m.renderPhaseBar(),
		convLabel,
		m.conv.View(),
	}
	if m.gateMsg != "" {
		banner := styleGateBanner.Width(m.width - 2).
			Render("⏸  " + m.gateMsg + "   ·   press Ctrl+A to approve")
		parts = append(parts, banner)
	}
	parts = append(parts, m.statusbar.View())

	mainView := strings.Join(parts, "\n")

	if m.confirmQuit {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
		brightStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
		confirmBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("203")).
			Padding(1, 3).
			Render(
				warnStyle.Render("  Quit orchestrator?") + "\n\n" +
					dimStyle.Render("  Press ") +
					brightStyle.Render("y") +
					dimStyle.Render(" or ") +
					brightStyle.Render("Enter") +
					dimStyle.Render(" to confirm, any other key to cancel"),
			)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, confirmBox)
	}

	return mainView
}

func (m *ControlModel) layout() {
	convH := m.height - 5
	if convH < 5 {
		convH = 5
	}
	m.conv.SetSize(m.width, convH)
	m.statusbar = m.statusbar.WithWidth(m.width)
}

// renderPhaseBar renders a one-line pipeline progress indicator with planning sub-stages.
func (m ControlModel) renderPhaseBar() string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	activeStyle := lipgloss.NewStyle().Foreground(roleColor("planner")).Bold(true)

	var parts []string

	for _, gate := range planningGates {
		label := gateLabel(gate)
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

	postPhases := []string{"coding", "testing", "reviewing", "fixing", "done"}
	for _, ph := range postPhases {
		if strings.Contains(m.phase, ph) {
			c := roleColor(phaseRole(ph))
			parts = append(parts, lipgloss.NewStyle().Foreground(c).Bold(true).Render("◉ "+ph))
		} else {
			parts = append(parts, dimStyle.Render("○ "+ph))
		}
	}

	return "  " + strings.Join(parts, sepStyle.Render(" → "))
}

func (m ControlModel) renderAgentSummary() string {
	var cells []string
	for _, role := range []bus.AgentRole{
		bus.RolePlanner, bus.RoleCoder, bus.RoleTester,
		bus.RoleReviewer, bus.RoleSecurity,
	} {
		cells = append(cells, renderAgentPill(role, m.agentStates[role]))
	}
	return strings.Join(cells, "  ")
}

func renderAgentPill(role bus.AgentRole, state AgentState) string {
	roleText := roleStyle(string(role)).Bold(true).Render(strings.ToUpper(string(role)))
	var stateText string
	switch state {
	case AgentRunning:
		stateText = styleRunning.Render("running")
	case AgentDone:
		stateText = styleDone.Render("done")
	case AgentError:
		stateText = styleError.Render("error")
	case AgentGate:
		stateText = styleGate.Render("gate")
	default:
		stateText = styleWaiting.Render("waiting")
	}
	return roleText + " " + stateText
}
