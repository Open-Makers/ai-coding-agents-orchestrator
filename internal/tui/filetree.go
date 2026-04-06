package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ftEntry struct {
	name  string
	isDir bool
}

// FileTreeModel is an MC-style single-pane directory navigator.
// Directories are navigable; .md files are selectable.
// Any file can be selected with Ctrl+Enter.
type FileTreeModel struct {
	root    string // repo root (never go above this)
	cwd     string // current directory being displayed
	entries []ftEntry
	cursor  int
	width   int
	height  int
}

func NewFileTree(root string) (FileTreeModel, error) {
	m := FileTreeModel{root: root, cwd: root, width: 80, height: 20}
	if err := m.reload(); err != nil {
		return m, err
	}
	return m, nil
}

// reload reads cwd and populates entries: dirs first (sorted), then files.
func (m *FileTreeModel) reload() error {
	entries, err := os.ReadDir(m.cwd)
	if err != nil {
		return err
	}

	m.entries = nil

	// ".." unless already at root
	if m.cwd != m.root {
		m.entries = append(m.entries, ftEntry{name: "..", isDir: true})
	}

	var dirs, files []ftEntry
	for _, e := range entries {
		name := e.Name()
		// skip common noise directories
		if name == "node_modules" || name == "vendor" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, ftEntry{name: name + "/", isDir: true})
		} else {
			files = append(files, ftEntry{name: name})
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	m.entries = append(m.entries, dirs...)
	m.entries = append(m.entries, files...)
	m.cursor = 0
	return nil
}

func (m FileTreeModel) Init() tea.Cmd { return nil }

func (m FileTreeModel) Update(msg tea.Msg) (FileTreeModel, tea.Cmd) {
	switch msg := msg.(type) {
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
			if e.isDir {
				next := m.cwdJoin(e.name)
				m.cwd = next
				_ = m.reload()
			} else {
				// select file
				path := filepath.Join(m.cwd, e.name)
				return m, func() tea.Msg { return FileSelectedMsg{Path: path} }
			}

		case "backspace", "left", "h":
			if m.cwd != m.root {
				parent := filepath.Dir(m.cwd)
				if !strings.HasPrefix(parent, m.root) {
					parent = m.root
				}
				prevDir := filepath.Base(m.cwd)
				m.cwd = parent
				_ = m.reload()
				// restore cursor on the dir we came from
				for i, e := range m.entries {
					if strings.TrimSuffix(e.name, "/") == prevDir {
						m.cursor = i
						break
					}
				}
			}

		case "esc":
			return m, func() tea.Msg { return EditorCancelledMsg{} }
		}
	}
	return m, nil
}

func (m FileTreeModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)
	mdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	cursorBg := lipgloss.NewStyle().
		Background(lipgloss.Color("238")).
		Foreground(lipgloss.Color("255")).
		Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sep := strings.Repeat("─", m.width)

	// header
	rel, _ := filepath.Rel(m.root, m.cwd)
	if rel == "." || rel == "" {
		rel = "/"
	} else {
		rel = "/" + rel + "/"
	}
	header := titleStyle.Render("Select requirements file") +
		"  " + pathStyle.Render(rel)

	var lines []string
	lines = append(lines, header, sep)

	rows := m.visibleRows()
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}

	for i := start; i < len(m.entries) && i < start+rows; i++ {
		e := m.entries[i]
		maxW := m.width - 4
		label := e.name
		if len(label) > maxW {
			label = label[:maxW-1] + "…"
		}

		var rendered string
		if e.isDir {
			rendered = dirStyle.Render("  " + label)
		} else if strings.HasSuffix(e.name, ".md") {
			rendered = mdStyle.Render("  " + label)
		} else {
			rendered = fileStyle.Render("  " + label)
		}

		if i == m.cursor {
			// pad to full width for highlight bar
			pad := m.width - 2 - lipglossLen(rendered)
			if pad < 0 {
				pad = 0
			}
			rendered = cursorBg.Render("▶ " + strings.TrimPrefix(rendered, "  ") + strings.Repeat(" ", pad))
		}
		lines = append(lines, rendered)
	}

	// scroll position hint when list is longer than screen
	if len(m.entries) > rows {
		pct := 0
		if len(m.entries) > 1 {
			pct = m.cursor * 100 / (len(m.entries) - 1)
		}
		lines = append(lines, dimStyle.Render(strings.Repeat("─", m.width-8)+"  "+strings.Repeat("░", pct/10)+strings.Repeat("·", 10-pct/10)))
	}

	lines = append(lines, sep)
	lines = append(lines, dimStyle.Render("↑↓/PgUp/PgDn navigate   Enter/→ open   ←/Backspace up   Esc back"))

	return strings.Join(lines, "\n")
}

func (m FileTreeModel) visibleRows() int {
	rows := m.height - 5 // header + sep + sep + hints
	if rows < 4 {
		rows = 4
	}
	return rows
}

func (m *FileTreeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// cwdJoin resolves a relative entry name (possibly "..") against cwd, clamped to root.
func (m *FileTreeModel) cwdJoin(name string) string {
	name = strings.TrimSuffix(name, "/")
	if name == ".." {
		p := filepath.Dir(m.cwd)
		if strings.HasPrefix(p, m.root) {
			return p
		}
		return m.root
	}
	return filepath.Join(m.cwd, name)
}
