package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

var agentOrder = []bus.AgentRole{
	bus.RolePlanner,
	bus.RoleCoder,
	bus.RoleTester,
	bus.RoleReviewer,
}

type overlayKind int

const (
	overlayNone   overlayKind = iota
	overlayPicker             // picker → optional editor
	overlayEditor
	overlayGit
	overlayChat
)

// Model is the root Bubble Tea model for the orchestrator TUI.
type Model struct {
	panels    map[bus.AgentRole]AgentPanelModel
	conv      ConversationModel
	statusbar StatusBarModel
	events    <-chan bus.Message
	pipeline  *orchestrator.Pipeline
	llm       runner.LLMRunner
	root      string
	wsPath    string
	width     int
	height    int
	quitting  bool

	// Typed overlay state — only one active at a time.
	overlay       overlayKind
	overlayPicker PickerModel
	overlayEditor EditorModel
	overlayGit    GitPanelModel
	overlayChat   ChatModel
}

// New creates the root TUI model.
// llm is used for the chat overlay (may be nil).
func New(events <-chan bus.Message, pipeline *orchestrator.Pipeline, root, wsPath string, llm runner.LLMRunner) Model {
	panels := make(map[bus.AgentRole]AgentPanelModel)
	for _, role := range agentOrder {
		panels[role] = NewAgentPanel(role)
	}
	return Model{
		panels:    panels,
		conv:      NewConversation(80, 10),
		statusbar: NewStatusBar(80).WithBranch(GitBranch(root)).WithState("idle"),
		events:    events,
		pipeline:  pipeline,
		llm:       llm,
		root:      root,
		wsPath:    wsPath,
		width:     80,
		height:    24,
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitForBusEvent(m.events)}
	for _, p := range m.panels {
		cmds = append(cmds, p.Init())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Route to overlay first.
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
			if m.pipeline != nil {
				m.pipeline.Approve()
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

	case BusMessageMsg:
		// Update status bar state.
		if bm := msg.Msg; bm.Type == bus.MsgEvent {
			if s, ok := bm.Payload.(string); ok {
				for _, ps := range []string{"planning", "coding", "testing", "reviewing", "fixing", "done"} {
					if strings.Contains(s, ps) {
						m.statusbar = m.statusbar.WithState(s)
					}
				}
			}
		}

		// Route to agent panels.
		agentMsg := busToAgentEvent(msg.Msg)
		if agentMsg.Role != "" {
			if p, ok := m.panels[agentMsg.Role]; ok {
				updated, cmd := p.Update(agentMsg)
				m.panels[agentMsg.Role] = updated
				cmds = append(cmds, cmd)
			}
		}

		// Update conversation.
		var cmd tea.Cmd
		m.conv, cmd = m.conv.Update(msg)
		cmds = append(cmds, cmd)

		// Continue listening.
		cmds = append(cmds, waitForBusEvent(m.events))

	case spinner.TickMsg:
		for role, p := range m.panels {
			updated, cmd := p.Update(msg)
			m.panels[role] = updated
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// updateOverlay dispatches to the currently active overlay.
func (m Model) updateOverlay(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.overlay {
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
	}

	return m, nil
}

func (m Model) View() string {
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
	}

	panelH := (m.height - 5) / 2 // leave room for conv + status bar
	panelW := m.width / 2

	// 2×2 agent grid
	row1 := m.renderRow([]bus.AgentRole{bus.RolePlanner, bus.RoleCoder}, panelW, panelH)
	row2 := m.renderRow([]bus.AgentRole{bus.RoleTester, bus.RoleReviewer}, panelW, panelH)

	convH := m.height - panelH*2 - 3
	if convH < 3 {
		convH = 3
	}

	convLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render("  Conversation")

	return strings.Join([]string{
		row1,
		row2,
		convLabel,
		m.conv.View(),
		m.statusbar.View(),
	}, "\n")
}

func (m *Model) layout() {
	panelH := (m.height - 5) / 2
	panelW := m.width / 2
	convH := m.height - panelH*2 - 3
	if convH < 3 {
		convH = 3
	}

	for role, p := range m.panels {
		p.SetSize(panelW, panelH)
		m.panels[role] = p
	}
	m.conv.SetSize(m.width, convH)
	m.statusbar = m.statusbar.WithWidth(m.width)
}

func (m Model) renderRow(roles []bus.AgentRole, w, h int) string {
	cells := make([]string, 0, len(roles))
	for _, role := range roles {
		p := m.panels[role]
		p.SetSize(w, h)
		cells = append(cells, p.View())
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cells...)
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
			if strings.Contains(p, "error") {
				state = AgentError
			}
		}
	}

	return AgentEventMsg{Role: role, Text: text, State: state}
}
