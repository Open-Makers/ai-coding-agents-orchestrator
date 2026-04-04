package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// ConvOnlyModel is a minimal Bubble Tea model for --ui=tmux mode.
// It shows only the Conversation Panel and Status Bar (no agent panels —
// those live in separate tmux panes).
type ConvOnlyModel struct {
	conv      ConversationModel
	statusbar StatusBarModel
	events    <-chan bus.Message
	width     int
	height    int
	quitting  bool
}

// NewConvOnly creates the conversation-only TUI for tmux main window.
func NewConvOnly(events <-chan bus.Message, root string) ConvOnlyModel {
	return ConvOnlyModel{
		conv:      NewConversation(80, 40),
		statusbar: NewStatusBar(80).WithBranch(GitBranch(root)).WithState("idle"),
		events:    events,
		width:     80,
		height:    50,
	}
}

func (m ConvOnlyModel) Init() tea.Cmd {
	return waitForBusEvent(m.events)
}

func (m ConvOnlyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.conv.SetSize(m.width, m.height-3)
		m.statusbar = m.statusbar.WithWidth(m.width)

	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "Q" {
			m.quitting = true
			return m, tea.Quit
		}

	case BusMessageMsg:
		if bm := msg.Msg; bm.Type == bus.MsgEvent {
			if s, ok := bm.Payload.(string); ok {
				m.statusbar = m.statusbar.WithState(s)
			}
		}
		var cmd tea.Cmd
		m.conv, cmd = m.conv.Update(msg)
		return m, tea.Batch(cmd, waitForBusEvent(m.events))
	}

	return m, nil
}

func (m ConvOnlyModel) View() string {
	if m.quitting {
		return "bye\n"
	}
	convLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render("  Conversation")
	return strings.Join([]string{
		convLabel,
		m.conv.View(),
		m.statusbar.View(),
	}, "\n")
}
