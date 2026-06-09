package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// projectNameDoneMsg is emitted when the user saves a new project name.
type projectNameDoneMsg struct{ name string }

// projectNameCancelledMsg is emitted when the user cancels without saving.
type projectNameCancelledMsg struct{}

// projectNameModel is a small standalone screen for setting the project name,
// persisted to .orchestrator/project.yaml.
type projectNameModel struct {
	input  textinput.Model
	width  int
	height int
}

func newProjectNameModel(current string) projectNameModel {
	ti := textinput.New()
	ti.CharLimit = 80
	ti.Prompt = ""
	ti.SetValue(current)
	ti.CursorEnd()
	ti.Focus()
	return projectNameModel{input: ti, width: 80, height: 24}
}

func (m projectNameModel) Init() tea.Cmd { return textinput.Blink }

func (m projectNameModel) Update(msg tea.Msg) (projectNameModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return projectNameCancelledMsg{} }
		case "enter", "ctrl+s":
			return m, func() tea.Msg {
				return projectNameDoneMsg{name: strings.TrimSpace(m.input.Value())}
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m projectNameModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(homePalette.accent)
	dimStyle := lipgloss.NewStyle().Foreground(homePalette.dim)

	var b strings.Builder
	b.WriteString(titleStyle.Render("📛 Project Name"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Set a display name for this project (saved to\n.orchestrator/project.yaml)."))
	b.WriteString("\n\n")
	b.WriteString("  Name:  ")
	b.WriteString(m.input.View())
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  enter/ctrl+s save   ·   esc cancel"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}
