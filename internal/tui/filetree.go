package tui

import (
	"fmt"
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
	dirStyle := lipgloss.NewStyle().Foreground(p.cyan).Bold(true)
	mdStyle := lipgloss.NewStyle().Foreground(p.bright)
	fileStyle := lipgloss.NewStyle().Foreground(p.dim)
	cursorBg := lipgloss.NewStyle().
		Background(p.activeBg).
		Foreground(p.green).
		Bold(true)
	goldStyle := lipgloss.NewStyle().Foreground(p.gold).Bold(true)

	// Header bar.
	rel, _ := filepath.Rel(m.root, m.cwd)
	if rel == "." || rel == "" {
		rel = "/"
	} else {
		rel = "/" + rel + "/"
	}
	header := headerStyle.Render("◆  orchestrator  ·  browse files  " + dimStyle.Render(rel))

	// Card content.
	cardW := m.width - 8
	if cardW < 40 {
		cardW = 40
	}
	if cardW > 90 {
		cardW = 90
	}

	var lines []string
	lines = append(lines, goldStyle.Render(" ◆ "+rel))
	lines = append(lines, "")

	rows := m.visibleRows() - 4
	if rows < 4 {
		rows = 4
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}

	for i := start; i < len(m.entries) && i < start+rows; i++ {
		e := m.entries[i]
		maxW := cardW - 10
		if maxW < 20 {
			maxW = 20
		}
		label := e.name
		if len(label) > maxW {
			label = label[:maxW-1] + "…"
		}

		var icon string
		var styled string
		if e.isDir {
			if e.name == ".." {
				icon = "↩"
			} else {
				icon = "📁"
			}
			styled = dirStyle.Render(label)
		} else if strings.HasSuffix(e.name, ".md") {
			icon = "📄"
			styled = mdStyle.Render(label)
		} else {
			icon = "  "
			styled = fileStyle.Render(label)
		}

		line := fmt.Sprintf("  %s %s", icon, styled)

		if i == m.cursor {
			padW := cardW - 8
			if padW < 20 {
				padW = 20
			}
			highlight := cursorBg.Width(padW).Render(
				fmt.Sprintf(" %s %s", icon, label),
			)
			line = lipgloss.NewStyle().Foreground(p.green).Bold(true).Render("▸") + highlight
		}

		lines = append(lines, line)
	}

	// Scrollbar indicator.
	if len(m.entries) > rows {
		pct := 0
		if len(m.entries) > 1 {
			pct = m.cursor * 100 / (len(m.entries) - 1)
		}
		barLen := 10
		filled := pct * barLen / 100
		if filled < 1 && pct > 0 {
			filled = 1
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barLen-filled)
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %s  %d/%d", bar, m.cursor+1, len(m.entries))))
	}

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.border).
		Padding(0, 1).
		Width(cardW).
		Render(strings.Join(lines, "\n"))

	vpH := m.height - 2
	if vpH < 4 {
		vpH = 4
	}
	body := lipgloss.Place(m.width, vpH, lipgloss.Center, lipgloss.Center, card)

	// Footer key hints.
	keyStyle := lipgloss.NewStyle().
		Background(p.footerBg).
		Foreground(p.accent).
		Bold(true)
	hint := func(k, desc string) string {
		return keyStyle.Render(k) + lipgloss.NewStyle().Background(p.footerBg).Foreground(p.dim).Render(" "+desc)
	}
	footer := footerStyle.Render(
		hint("↑↓", "navigate") + "  " +
			hint("Enter/→", "open") + "  " +
			hint("←", "back") + "  " +
			hint("PgUp/Dn", "page") + "  " +
			hint("Esc", "cancel"),
	)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
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
