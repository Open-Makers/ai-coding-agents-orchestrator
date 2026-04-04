package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

const maxConvLines = 200

// ConversationModel shows a scrollable log of bus messages.
type ConversationModel struct {
	vp    viewport.Model
	lines []string
}

func NewConversation(width, height int) ConversationModel {
	vp := viewport.New(width, height)
	return ConversationModel{vp: vp}
}

func (m ConversationModel) Init() tea.Cmd { return nil }

func (m ConversationModel) Update(msg tea.Msg) (ConversationModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case BusMessageMsg:
		line := formatConvLine(msg.Msg)
		if line != "" {
			m.addLine(line)
		}
	case tea.WindowSizeMsg:
		m.vp.Width = msg.Width
		m.vp.Height = msg.Height
	}
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m ConversationModel) View() string {
	return m.vp.View()
}

func (m *ConversationModel) SetSize(w, h int) {
	m.vp.Width = w
	m.vp.Height = h
}

func (m *ConversationModel) addLine(line string) {
	if len(m.lines) >= maxConvLines {
		m.lines = m.lines[1:]
	}
	m.lines = append(m.lines, line)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
}

func formatConvLine(msg bus.Message) string {
	// Skip individual token stream events — too noisy.
	if p, ok := msg.Payload.(bus.TokenPayload); ok {
		_ = p
		return ""
	}

	ts := styleConvTimestamp.Render(msg.Timestamp.Format("15:04:05"))
	from := roleStyle(string(msg.From)).Render(padRole(string(msg.From)))
	to := styleConvTimestamp.Render(padRole(string(msg.To)))

	var summary string
	switch p := msg.Payload.(type) {
	case string:
		summary = truncate(p, 70)
	default:
		summary = truncate(fmt.Sprintf("%v", p), 70)
	}

	if summary == "" {
		return ""
	}

	return fmt.Sprintf("%s  %s → %s  %s", ts, from, to, summary)
}

func roleStyle(role string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(roleColor(role))
}

func padRole(r string) string {
	const width = 8
	if len(r) >= width {
		return r[:width]
	}
	return r + strings.Repeat(" ", width-len(r))
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
