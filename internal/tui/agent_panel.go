package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// AgentPanelModel displays the status and recent output of one agent.
type AgentPanelModel struct {
	role      bus.AgentRole
	state     AgentState
	lastLines []string // ring of last 3 output lines
	elapsed   string
	spinner   spinner.Model
	width     int
	height    int
}

func NewAgentPanel(role bus.AgentRole) AgentPanelModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleRunning
	return AgentPanelModel{
		role:    role,
		state:   AgentWaiting,
		spinner: s,
		width:   30,
		height:  6,
	}
}

func (m AgentPanelModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m AgentPanelModel) Update(msg tea.Msg) (AgentPanelModel, tea.Cmd) {
	switch msg := msg.(type) {
	case AgentEventMsg:
		if msg.Role != m.role {
			return m, nil
		}
		if msg.State != "" {
			m.state = msg.State
		}
		if msg.Text != "" {
			m.addLine(msg.Text)
		}
		if m.state == AgentRunning {
			return m, m.spinner.Tick
		}
		return m, nil

	case spinner.TickMsg:
		if m.state != AgentRunning {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *AgentPanelModel) addLine(text string) {
	// flatten multi-line tokens into individual lines
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(m.lastLines) >= 3 {
			m.lastLines = m.lastLines[1:]
		}
		m.lastLines = append(m.lastLines, line)
	}
}

func (m AgentPanelModel) View() string {
	inner := m.renderInner()
	color := roleColor(string(m.role))

	border := stylePanelBorder.Copy().
		BorderForeground(color).
		Width(m.width - 2).
		Height(m.height - 2)

	return border.Render(inner)
}

func (m AgentPanelModel) renderInner() string {
	roleStyle := lipgloss.NewStyle().
		Foreground(roleColor(string(m.role))).
		Bold(true)

	header := roleStyle.Render(fmt.Sprintf("[%s]", strings.ToUpper(string(m.role))))

	var statusIcon string
	switch m.state {
	case AgentWaiting:
		statusIcon = styleWaiting.Render("○ waiting")
	case AgentRunning:
		statusIcon = m.spinner.View() + styleRunning.Render(" running")
	case AgentDone:
		statusIcon = styleDone.Render("✓ done")
	case AgentError:
		statusIcon = styleError.Render("✗ error")
	case AgentGate:
		statusIcon = styleGate.Render("⏸ waiting for approval")
	}

	lines := []string{header + "  " + statusIcon}

	for _, l := range m.lastLines {
		truncated := l
		maxW := m.width - 4
		if len(truncated) > maxW {
			truncated = truncated[:maxW-1] + "…"
		}
		lines = append(lines, styleWaiting.Render(truncated))
	}

	return strings.Join(lines, "\n")
}

// SetSize updates panel dimensions.
func (m *AgentPanelModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}
