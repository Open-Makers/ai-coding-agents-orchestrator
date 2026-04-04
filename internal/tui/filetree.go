package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FileTreeModel is a filterable list of .md files from the git repo.
type FileTreeModel struct {
	root        string
	allFiles    []string
	filtered    []string
	filter      string
	cursor      int
	previewText string
	showPreview bool
	width       int
	height      int
}

func NewFileTree(root string) (FileTreeModel, error) {
	files, err := listMdFiles(root)
	if err != nil {
		return FileTreeModel{}, err
	}
	return FileTreeModel{
		root:     root,
		allFiles: files,
		filtered: files,
		width:    80,
		height:   20,
	}, nil
}

func (m FileTreeModel) Init() tea.Cmd { return nil }

func (m FileTreeModel) Update(msg tea.Msg) (FileTreeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.loadPreview()
			}
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.loadPreview()
			}
		case "ctrl+p":
			m.showPreview = !m.showPreview
		case "enter":
			if len(m.filtered) > 0 {
				return m, func() tea.Msg {
					return FileSelectedMsg{Path: filepath.Join(m.root, m.filtered[m.cursor])}
				}
			}
		case "esc", "backspace":
			if m.filter != "" {
				m.filter = m.filter[:len(m.filter)-1]
				m.applyFilter()
			} else {
				return m, func() tea.Msg { return EditorCancelledMsg{} }
			}
		default:
			if len(msg.String()) == 1 {
				m.filter += msg.String()
				m.applyFilter()
			}
		}
	}
	return m, nil
}

func (m FileTreeModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	header := titleStyle.Render("Pick requirements file") +
		"   Filter: " + filterStyle.Render(m.filter+"█")

	listW := m.width
	if m.showPreview {
		listW = m.width / 2
	}

	var sb strings.Builder
	sb.WriteString(header + "\n")
	sb.WriteString(strings.Repeat("─", m.width) + "\n")

	maxItems := m.height - 4
	start := 0
	if m.cursor >= maxItems {
		start = m.cursor - maxItems + 1
	}

	for i := start; i < len(m.filtered) && i < start+maxItems; i++ {
		f := m.filtered[i]
		if len(f) > listW-4 {
			f = "…" + f[len(f)-(listW-5):]
		}
		if i == m.cursor {
			sb.WriteString(cursorStyle.Render("► " + f))
		} else {
			sb.WriteString(dimStyle.Render("  " + f))
		}
		sb.WriteString("\n")
	}

	list := sb.String()

	if m.showPreview && m.previewText != "" {
		preview := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1).
			Width(m.width/2 - 2).
			Render(truncate(m.previewText, (m.width/2-4)*(m.height-4)))

		return lipgloss.JoinHorizontal(lipgloss.Top, list, preview)
	}

	return list + dimStyle.Render("Enter select  Ctrl+P preview  Esc back")
}

func (m *FileTreeModel) applyFilter() {
	if m.filter == "" {
		m.filtered = m.allFiles
		m.cursor = 0
		return
	}
	var out []string
	lower := strings.ToLower(m.filter)
	for _, f := range m.allFiles {
		if strings.Contains(strings.ToLower(f), lower) {
			out = append(out, f)
		}
	}
	m.filtered = out
	m.cursor = 0
	m.loadPreview()
}

func (m *FileTreeModel) loadPreview() {
	if len(m.filtered) == 0 {
		m.previewText = ""
		return
	}
	path := filepath.Join(m.root, m.filtered[m.cursor])
	data, err := os.ReadFile(path)
	if err != nil {
		m.previewText = "(cannot read file)"
		return
	}
	m.previewText = string(data)
}

func (m *FileTreeModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func listMdFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "*.md", "**/*.md")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// fallback: walk the directory
		return walkMd(root)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func walkMd(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && (info.Name() == ".git" || info.Name() == ".orchestrator") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			rel, _ := filepath.Rel(root, path)
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}
