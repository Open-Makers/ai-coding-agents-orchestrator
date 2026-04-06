package tui

import (
	"fmt"
	"path/filepath"
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

	p := homePalette

	headerStyle := lipgloss.NewStyle().
		Background(p.headerBg).
		Foreground(p.accent).
		Bold(true).
		Padding(0, 2).
		Width(m.width)

	footerStyle := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.dim).
		Padding(0, 1).
		Width(m.width)

	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	greenStyle := lipgloss.NewStyle().Foreground(p.green).Bold(true)
	brightStyle := lipgloss.NewStyle().Foreground(p.bright)
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)
	cyanStyle := lipgloss.NewStyle().Foreground(p.cyan)
	dateStyle := lipgloss.NewStyle().Foreground(p.dim).Italic(true)

	header := headerStyle.Render("◆  orchestrator  ·  select requirements")

	// Build menu card content.
	cardW := m.width - 8
	if cardW < 40 {
		cardW = 40
	}
	if cardW > 80 {
		cardW = 80
	}
	innerW := cardW - 6
	if innerW < 20 {
		innerW = 20
	}

	var lines []string
	lines = append(lines, goldStyle.Render(" ◆ Requirements Source"))
	lines = append(lines, "")

	for i, item := range m.items {
		var icon, label string
		switch {
		case item.isNew:
			icon = "✚"
			label = "New — open editor"
		case item.isPicker:
			icon = "📂"
			label = "Pick file from repo…"
		default:
			icon = "◇"
			label = m.shortenRecentPath(item.path)
		}

		suffix := ""
		if item.date != "" {
			suffix = "  " + dateStyle.Render(item.date)
		}

		if i == m.cursor {
			activeLine := lipgloss.NewStyle().
				Background(p.activeBg).
				Foreground(p.green).
				Bold(true).
				Width(innerW).
				Render(fmt.Sprintf(" %s %s", icon, label))
			lines = append(lines, greenStyle.Render("▸")+activeLine+suffix)
			if item.isNew {
				lines = append(lines, "  "+dimStyle.Render("  Create requirements from scratch"))
			} else if item.isPicker {
				lines = append(lines, "  "+dimStyle.Render("  Browse repository files"))
			} else {
				lines = append(lines, "  "+dimStyle.Render("  Re-use previous requirements"))
			}
		} else {
			var styledLabel string
			if item.isNew {
				styledLabel = cyanStyle.Render(fmt.Sprintf("  %s %s", icon, label))
			} else if item.isPicker {
				styledLabel = brightStyle.Render(fmt.Sprintf("  %s %s", icon, label))
			} else {
				styledLabel = dimStyle.Render(fmt.Sprintf("  %s %s", icon, label))
			}
			lines = append(lines, styledLabel+suffix)
		}
		if i < len(m.items)-1 {
			lines = append(lines, "")
		}
	}

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(1, 2).
		Width(cardW).
		Render(strings.Join(lines, "\n"))

	if m.treeErr != "" {
		errBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("203")).
			Foreground(lipgloss.Color("203")).
			Padding(0, 2).
			Width(cardW).
			Render("⚠  " + m.treeErr)
		card = lipgloss.JoinVertical(lipgloss.Left, card, "", errBox)
	}

	// Center card in available space.
	vpH := m.height - 2
	if vpH < 4 {
		vpH = 4
	}
	body := lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Center, card)

	// Footer with key hints.
	keyStyle := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.accent).
		Bold(true)
	hintText := func(k, desc string) string {
		return keyStyle.Render(k) + lipgloss.NewStyle().Background(p.footerBg).Foreground(p.dim).Render(" "+desc)
	}
	footer := footerStyle.Render(
		hintText("↑↓", "navigate") + "  " +
			hintText("Enter", "select") + "  " +
			hintText("q", "quit"),
	)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m PickerModel) shortenRecentPath(path string) string {
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		return filepath.Base(path)
	}
	if len(rel) > 45 {
		return "…" + rel[len(rel)-44:]
	}
	return rel
}

func (m *PickerModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}
