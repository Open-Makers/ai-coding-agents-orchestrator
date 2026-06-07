package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

// TaskHistoryClosedMsg is emitted when the task history view is dismissed.
type TaskHistoryClosedMsg struct{}

// TaskHistoryModel is a read-only list of completed orchestrator tasks.
type TaskHistoryModel struct {
	tasks  []orchestrator.DoneTask
	cursor int
	width  int
	height int
}

// NewTaskHistory creates a history view for the given completed tasks.
func NewTaskHistory(tasks []orchestrator.DoneTask) TaskHistoryModel {
	return TaskHistoryModel{tasks: tasks, width: 80, height: 24}
}

func (m TaskHistoryModel) Init() tea.Cmd { return nil }

func (m *TaskHistoryModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m TaskHistoryModel) Update(msg tea.Msg) (TaskHistoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "enter":
			return m, func() tea.Msg { return TaskHistoryClosedMsg{} }
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.tasks)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m TaskHistoryModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#73daca")).Bold(true)
	selStyle := lipgloss.NewStyle().Reverse(true)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Done Tasks") + "  " + dimStyle.Render("↑↓ scroll · Esc back"))
	b.WriteString("\n" + strings.Repeat("─", m.width) + "\n")

	if len(m.tasks) == 0 {
		b.WriteString(dimStyle.Render("  No completed tasks yet."))
		return b.String()
	}

	for i, t := range m.tasks {
		if i == m.cursor {
			b.WriteString(selStyle.Render("  "+t.Title+"  ("+t.ID+")") + "\n")
		} else {
			b.WriteString("  " + doneStyle.Render("✓ ") + t.Title + " " + dimStyle.Render("("+t.ID+")") + "\n")
		}
	}
	return b.String()
}
