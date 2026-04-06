package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ProjectSelectedMsg is sent when user picks a project directory.
type ProjectSelectedMsg struct {
	Path string
}

// ProjectPickerCancelledMsg is sent when user cancels the project picker.
type ProjectPickerCancelledMsg struct{}

type projectPickerMode int

const (
	projectPickerModeList   projectPickerMode = iota
	projectPickerModeBrowse                   // filesystem directory browser
)

// ProjectPickerModel shows the project selection screen with current directory,
// an "Open project…" browser, and a list of recently opened projects.
type ProjectPickerModel struct {
	projects   []RecentProject
	cursor     int
	currentDir string
	confirmDel int // index in projects pending deletion, -1 = none
	mode       projectPickerMode
	dirBrowser DirBrowserModel
	width      int
	height     int
}

// NewProjectPicker creates the project selection screen.
func NewProjectPicker(currentDir string) ProjectPickerModel {
	return ProjectPickerModel{
		projects:   LoadRecentProjects(),
		currentDir: currentDir,
		confirmDel: -1,
		width:      80,
		height:     24,
	}
}

func (m ProjectPickerModel) Init() tea.Cmd { return nil }

// totalItems returns the number of selectable rows:
// [0] current dir, [1] open project…, [2..] recent projects.
func (m ProjectPickerModel) totalItems() int {
	return 2 + len(m.projects)
}

func (m ProjectPickerModel) Update(msg tea.Msg) (ProjectPickerModel, tea.Cmd) {
	// Directory browser sub-mode.
	if m.mode == projectPickerModeBrowse {
		switch msg := msg.(type) {
		case DirSelectedMsg:
			return m, func() tea.Msg { return ProjectSelectedMsg(msg) }
		case DirBrowserCancelledMsg:
			m.mode = projectPickerModeList
			return m, nil
		}
		var cmd tea.Cmd
		m.dirBrowser, cmd = m.dirBrowser.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		// Deletion confirmation intercepts all keys.
		if m.confirmDel >= 0 {
			switch msg.String() {
			case "y", "Y":
				if m.confirmDel < len(m.projects) {
					_ = RemoveRecentProject(m.projects[m.confirmDel].Path)
					m.projects = LoadRecentProjects()
					if m.cursor >= m.totalItems() {
						m.cursor = m.totalItems() - 1
					}
					if m.cursor < 0 {
						m.cursor = 0
					}
				}
				m.confirmDel = -1
			default:
				m.confirmDel = -1
			}
			return m, nil
		}

		total := m.totalItems()

		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < total-1 {
				m.cursor++
			}
		case "enter", " ":
			switch m.cursor {
			case 0:
				// Use current directory.
				dir := m.currentDir
				return m, func() tea.Msg { return ProjectSelectedMsg{Path: dir} }
			case 1:
				// Open project… → directory browser.
				browser, err := NewDirBrowser()
				if err == nil {
					browser.SetSize(m.width, m.height)
					m.dirBrowser = browser
					m.mode = projectPickerModeBrowse
				}
				return m, nil
			default:
				// Recent project selected.
				idx := m.cursor - 2
				if idx < len(m.projects) {
					proj := m.projects[idx]
					if dirExists(proj.Path) {
						path := proj.Path
						return m, func() tea.Msg { return ProjectSelectedMsg{Path: path} }
					}
				}
			}
		case "d", "D", "delete", "backspace":
			// Delete from recent list (only for recent project entries).
			idx := m.cursor - 2
			if idx >= 0 && idx < len(m.projects) {
				m.confirmDel = idx
			}
		case "q", "Q", "esc":
			return m, func() tea.Msg { return ProjectPickerCancelledMsg{} }
		}
	}
	return m, nil
}

func (m ProjectPickerModel) View() string {
	if m.mode == projectPickerModeBrowse {
		return m.dirBrowser.View()
	}

	p := homePalette
	titleStyle := lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(p.dim)
	activeStyle := lipgloss.NewStyle().Foreground(p.green).Bold(true)
	warnStyle := lipgloss.NewStyle().Foreground(p.red).Bold(true)
	browseStyle := lipgloss.NewStyle().Foreground(p.cyan)

	var lines []string
	lines = append(lines, "")
	lines = append(lines, titleStyle.Render("  ◈ SELECT PROJECT"))
	lines = append(lines, "")

	// --- Item 0: Current directory ---
	currentLabel := "📂 Use current directory"
	currentPath := dimStyle.Render("     " + m.currentDir)
	if m.cursor == 0 {
		lines = append(lines, activeStyle.Render("  ▸ "+currentLabel))
		lines = append(lines, "  "+currentPath)
	} else {
		lines = append(lines, "    "+currentLabel)
		lines = append(lines, "  "+currentPath)
	}
	lines = append(lines, "")

	// --- Item 1: Open project… ---
	openLabel := "📂 Open project…"
	if m.cursor == 1 {
		lines = append(lines, activeStyle.Render("  ▸ "+openLabel))
		lines = append(lines, "  "+dimStyle.Render("     Browse filesystem for a project directory"))
	} else {
		lines = append(lines, "    "+browseStyle.Render(openLabel))
		lines = append(lines, "  "+dimStyle.Render("     Browse filesystem for a project directory"))
	}
	lines = append(lines, "")

	// --- Items 2+: Recent projects ---
	if len(m.projects) > 0 {
		lines = append(lines, dimStyle.Render("  ── RECENT PROJECTS ──"))
		lines = append(lines, "")
		for i, proj := range m.projects {
			exists := dirExists(proj.Path)

			if m.confirmDel == i {
				lines = append(lines, warnStyle.Render(fmt.Sprintf(
					"    ✗ Remove %s? (y/N)", proj.Name)))
				lines = append(lines, "")
				continue
			}

			label := fmt.Sprintf("📁 %s", proj.Name)
			detail := dimStyle.Render("     " + proj.Path)
			if !exists {
				label = fmt.Sprintf("⊘ %s", proj.Name)
				detail = dimStyle.Render("     " + proj.Path + " (not found)")
			}

			if m.cursor == i+2 {
				lines = append(lines, activeStyle.Render("  ▸ "+label))
			} else {
				lines = append(lines, "    "+label)
			}
			lines = append(lines, "  "+detail)
			if i < len(m.projects)-1 {
				lines = append(lines, "")
			}
		}
	} else {
		lines = append(lines, dimStyle.Render("  No recent projects"))
	}

	lines = append(lines, "")
	sep := dimStyle.Render(strings.Repeat("─", m.width-4))
	lines = append(lines, sep)

	hints := dimStyle.Render("  ↑↓ navigate  Enter select  d remove  Esc back")
	lines = append(lines, hints)

	content := strings.Join(lines, "\n")
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, content)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
