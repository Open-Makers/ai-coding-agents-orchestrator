package tui

import (
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// EditorModel is a full-screen requirements editor.
type EditorModel struct {
	ta     textarea.Model
	width  int
	height int
}

func NewEditor(initialContent string) EditorModel {
	ta := textarea.New()
	ta.Placeholder = "# Task\nDescribe what you want to implement…"
	ta.SetValue(initialContent)
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(20)

	return EditorModel{ta: ta, width: 80, height: 24}
}

func (m EditorModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m EditorModel) Update(msg tea.Msg) (EditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ta.SetWidth(msg.Width - 4)
		m.ta.SetHeight(msg.Height - 4)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+s":
			content := m.ta.Value()
			return m, func() tea.Msg {
				return RequirementsSavedMsg{Path: content}
			}
		case "ctrl+e":
			content := m.ta.Value()
			return m, openExternalEditor(content)
		case "esc":
			return m, func() tea.Msg { return EditorCancelledMsg{} }
		}
	}

	// externalEditorResult is returned by openExternalEditor cmd.
	if result, ok := msg.(externalEditorResult); ok {
		if result.content != "" {
			m.ta.SetValue(result.content)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m EditorModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	title := titleStyle.Render("Requirements Editor")
	sep := strings.Repeat("─", m.width)
	hints := dimStyle.Render("Ctrl+S save & start   Ctrl+E open $EDITOR   Esc back")

	return strings.Join([]string{title, sep, m.ta.View(), sep, hints}, "\n")
}

// externalEditorResult carries content back from the external editor.
type externalEditorResult struct{ content string }

// openExternalEditor writes content to a temp file, opens $EDITOR, reads it back.
func openExternalEditor(content string) tea.Cmd {
	return func() tea.Msg {
		tmp, err := os.CreateTemp("", "orchestrator-req-*.md")
		if err != nil {
			return externalEditorResult{content: content}
		}
		defer os.Remove(tmp.Name())

		if _, err := tmp.WriteString(content); err != nil {
			tmp.Close()
			return externalEditorResult{content: content}
		}
		tmp.Close()

		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}

		cmd := exec.Command(editor, tmp.Name())
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// tea.ExecProcess would be cleaner but requires program reference.
		// For now use plain exec — TUI will be suspended while editor runs.
		_ = cmd.Run()

		data, err := os.ReadFile(tmp.Name())
		if err != nil {
			return externalEditorResult{content: content}
		}
		return externalEditorResult{content: string(data)}
	}
}
