package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DirSelectedMsg is emitted when a directory is chosen as a project.
type DirSelectedMsg struct {
	Path string
}

// DirBrowserCancelledMsg is emitted when the user cancels browsing.
type DirBrowserCancelledMsg struct{}

type dirEntry struct {
	name      string
	isProject bool
}

var projectMarkers = []string{
	".git", "go.mod", "package.json", "Cargo.toml",
	"pom.xml", "pyproject.toml", "setup.py", "Gemfile",
}

// DirBrowserModel is a filesystem navigator that shows only directories.
// Directories detected as projects (containing .git, go.mod, etc.) are
// highlighted and selectable with Enter. Regular directories are entered.
type DirBrowserModel struct {
	cwd        string
	entries    []dirEntry
	cursor     int
	showHidden bool
	width      int
	height     int
}

// NewDirBrowser creates a directory browser starting at $HOME.
func NewDirBrowser() (DirBrowserModel, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	m := DirBrowserModel{cwd: home, width: 80, height: 24}
	if err := m.reload(); err != nil {
		return m, err
	}
	return m, nil
}

func detectProject(path string) bool {
	for _, marker := range projectMarkers {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func (m *DirBrowserModel) reload() error {
	rawEntries, err := os.ReadDir(m.cwd)
	if err != nil {
		return err
	}

	m.entries = nil

	if m.cwd != "/" {
		m.entries = append(m.entries, dirEntry{name: ".."})
	}

	var dirs []dirEntry
	for _, e := range rawEntries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !m.showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if name == "node_modules" || name == "vendor" || name == "__pycache__" {
			continue
		}
		fullPath := filepath.Join(m.cwd, name)
		dirs = append(dirs, dirEntry{
			name:      name,
			isProject: detectProject(fullPath),
		})
	}

	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].isProject != dirs[j].isProject {
			return dirs[i].isProject
		}
		return dirs[i].name < dirs[j].name
	})

	m.entries = append(m.entries, dirs...)
	m.cursor = 0
	return nil
}

func (m DirBrowserModel) Init() tea.Cmd { return nil }

func (m DirBrowserModel) Update(msg tea.Msg) (DirBrowserModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "pgup":
			m.cursor -= m.visibleRows()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += m.visibleRows()
			if m.cursor >= len(m.entries) {
				m.cursor = len(m.entries) - 1
			}

		case "enter", "right", "l":
			if len(m.entries) == 0 {
				break
			}
			e := m.entries[m.cursor]
			if e.name == ".." {
				prevDir := filepath.Base(m.cwd)
				m.cwd = filepath.Dir(m.cwd)
				_ = m.reload()
				for i, re := range m.entries {
					if re.name == prevDir {
						m.cursor = i
						break
					}
				}
				return m, nil
			}

			fullPath := filepath.Join(m.cwd, e.name)
			if e.isProject {
				return m, func() tea.Msg { return DirSelectedMsg{Path: fullPath} }
			}
			m.cwd = fullPath
			_ = m.reload()

		case "o", "O":
			// Force-select current highlighted directory as project.
			if len(m.entries) == 0 {
				break
			}
			e := m.entries[m.cursor]
			if e.name == ".." {
				// Select current cwd itself.
				cwd := m.cwd
				return m, func() tea.Msg { return DirSelectedMsg{Path: cwd} }
			}
			fullPath := filepath.Join(m.cwd, e.name)
			return m, func() tea.Msg { return DirSelectedMsg{Path: fullPath} }

		case "backspace", "left", "h":
			if m.cwd != "/" {
				prevDir := filepath.Base(m.cwd)
				m.cwd = filepath.Dir(m.cwd)
				_ = m.reload()
				for i, re := range m.entries {
					if re.name == prevDir {
						m.cursor = i
						break
					}
				}
			}

		case ".":
			m.showHidden = !m.showHidden
			_ = m.reload()

		case "~":
			if home, err := os.UserHomeDir(); err == nil {
				m.cwd = home
				_ = m.reload()
			}

		case "esc", "q":
			return m, func() tea.Msg { return DirBrowserCancelledMsg{} }
		}
	}
	return m, nil
}

func (m DirBrowserModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	pathStyle := lipgloss.NewStyle().Foreground(crt.dim)
	projectStyle := lipgloss.NewStyle().Foreground(crt.success).Bold(true)
	dirStyle := lipgloss.NewStyle().Foreground(crt.primary)
	cursorBg := lipgloss.NewStyle().
		Background(crt.muted).
		Foreground(crt.bright).
		Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	sep := strings.Repeat("─", m.width)

	header := titleStyle.Render("Open Project") + "  " + pathStyle.Render(m.cwd)

	var lines []string
	lines = append(lines, header, sep)

	rows := m.visibleRows()
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}

	for i := start; i < len(m.entries) && i < start+rows; i++ {
		e := m.entries[i]
		maxW := m.width - 6

		label := e.name + "/"
		if e.name == ".." {
			label = ".."
		}
		if len(label) > maxW {
			label = label[:maxW-1] + "…"
		}

		var rendered string
		switch {
		case e.isProject:
			rendered = projectStyle.Render("  ◆ " + label)
		case e.name == "..":
			rendered = dirStyle.Render("  ↑ " + label)
		default:
			rendered = dirStyle.Render("  ▪ " + label)
		}

		if i == m.cursor {
			pad := m.width - 2 - lipglossLen(rendered)
			if pad < 0 {
				pad = 0
			}
			rendered = cursorBg.Render("▶ " + strings.TrimPrefix(rendered, "  ") + strings.Repeat(" ", pad))
		}
		lines = append(lines, rendered)
	}

	if len(m.entries) > rows {
		pct := 0
		if len(m.entries) > 1 {
			pct = m.cursor * 100 / (len(m.entries) - 1)
		}
		lines = append(lines, dimStyle.Render(
			strings.Repeat("─", m.width-8)+"  "+
				strings.Repeat("░", pct/10)+strings.Repeat("·", 10-pct/10)))
	}

	lines = append(lines, sep)

	legend := "↑↓ navigate  Enter "
	if len(m.entries) > 0 && m.cursor < len(m.entries) && m.entries[m.cursor].isProject {
		legend += "select"
	} else {
		legend += "open"
	}
	legend += "  o force-select  . hidden  ~ home  ← back  Esc cancel"
	lines = append(lines, dimStyle.Render(legend))

	return strings.Join(lines, "\n")
}

func (m DirBrowserModel) visibleRows() int {
	rows := m.height - 5
	if rows < 4 {
		rows = 4
	}
	return rows
}

// SetSize updates the browser dimensions.
func (m *DirBrowserModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}
