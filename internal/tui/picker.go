package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type pickerMode int

const (
	pickerModeStart    pickerMode = iota
	pickerModeFileTree pickerMode = iota
)

type pickerItem struct {
	label    string
	path     string
	date     string
	isNew    bool
	isPicker bool
}

// PickerModel is the start screen for selecting requirements.
type PickerModel struct {
	items   []pickerItem
	cursor  int
	mode    pickerMode
	tree    FileTreeModel
	treeErr string
	root    string
	wsPath  string
	width   int
	height  int
}

func NewPicker(root, wsPath string) PickerModel {
	items := []pickerItem{
		{label: "New — open editor", isNew: true},
		{label: "Pick file from repo…", isPicker: true},
	}

	for _, path := range LoadHistory(wsPath) {
		items = append(items, pickerItem{
			label: "Recent: " + path,
			path:  path,
			date:  time.Now().Format("2006-01-02"), // placeholder; history has no timestamps
		})
	}

	return PickerModel{
		items:  items,
		root:   root,
		wsPath: wsPath,
		width:  80,
		height: 20,
	}
}

func (m PickerModel) Init() tea.Cmd { return nil }

func (m PickerModel) Update(msg tea.Msg) (PickerModel, tea.Cmd) {
	if m.mode == pickerModeFileTree {
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg)

		// Handle tree selection.
		switch msg := msg.(type) {
		case FileSelectedMsg:
			_ = SaveHistory(m.wsPath, msg.Path)
			return m, func() tea.Msg {
				return PickerSelectedMsg{Path: msg.Path}
			}
		case EditorCancelledMsg:
			m.mode = pickerModeStart
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			item := m.items[m.cursor]
			switch {
			case item.isNew:
				return m, func() tea.Msg { return PickerSelectedMsg{IsNew: true} }
			case item.isPicker:
				tree, err := NewFileTree(m.root)
				if err != nil {
					m.treeErr = err.Error()
				} else {
					tree.SetSize(m.width, m.height)
					m.tree = tree
					m.mode = pickerModeFileTree
				}
			default:
				_ = SaveHistory(m.wsPath, item.path)
				return m, func() tea.Msg { return PickerSelectedMsg{Path: item.path} }
			}
		case "q":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.tree.SetSize(msg.Width, msg.Height)
	}
	return m, nil
}

func (m PickerModel) View() string {
	if m.mode == pickerModeFileTree {
		return m.tree.View()
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("orchestrator  ·  select requirements") + "\n")
	sb.WriteString(strings.Repeat("─", m.width) + "\n\n")

	for i, item := range m.items {
		line := item.label
		suffix := ""
		if item.date != "" {
			suffix = dateStyle.Render(fmt.Sprintf("  %s", item.date))
		}
		if i == m.cursor {
			sb.WriteString(cursorStyle.Render("► "+line) + suffix + "\n")
		} else {
			sb.WriteString(dimStyle.Render("  "+line) + suffix + "\n")
		}
	}

	if m.treeErr != "" {
		sb.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("error: "+m.treeErr) + "\n")
	}

	sb.WriteString("\n" + dimStyle.Render("↑↓ navigate   Enter select   q quit"))
	return sb.String()
}

func (m *PickerModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}
