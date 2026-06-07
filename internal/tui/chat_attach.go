package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// maxAttachBytes caps how much of a single .md file is injected as context,
// guarding against accidentally attaching a huge generated document.
const maxAttachBytes = 64 * 1024

// chatAttachment is a markdown file selected to enrich the chat context.
type chatAttachment struct {
	rel     string // path relative to root, used for display
	content string
}

// mdPicker is a compact multi-select list of markdown files used to attach
// repository context to the PM chat.
type mdPicker struct {
	root     string
	files    []string        // relative paths
	selected map[string]bool // keyed by relative path
	cursor   int
	width    int
	height   int
}

// mdPickerDoneMsg is emitted when the user confirms or cancels the picker.
type mdPickerDoneMsg struct {
	selected []string // relative paths; nil when cancelled
	cancel   bool
}

func newMDPicker(root string, already []chatAttachment, w, h int) mdPicker {
	files := findMarkdownFiles(root)
	selected := make(map[string]bool, len(already))
	for _, a := range already {
		selected[a.rel] = true
	}
	return mdPicker{
		root:     root,
		files:    files,
		selected: selected,
		width:    w,
		height:   h,
	}
}

// findMarkdownFiles walks root and returns markdown paths relative to root,
// skipping noisy directories. Results are sorted for stable display.
func findMarkdownFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".md" || ext == ".markdown" {
			if rel, rerr := filepath.Rel(root, path); rerr == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (m mdPicker) Update(msg tea.Msg) (mdPicker, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return mdPickerDoneMsg{cancel: true} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
	case " ", "space":
		if len(m.files) > 0 {
			rel := m.files[m.cursor]
			if m.selected[rel] {
				delete(m.selected, rel)
			} else {
				m.selected[rel] = true
			}
		}
	case "enter":
		var sel []string
		for _, f := range m.files {
			if m.selected[f] {
				sel = append(sel, f)
			}
		}
		return m, func() tea.Msg { return mdPickerDoneMsg{selected: sel} }
	}
	return m, nil
}

func (m mdPicker) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	brightStyle := lipgloss.NewStyle().Foreground(crt.bright)

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Attach .md context"))
	sb.WriteString("  " + dimStyle.Render("Space toggle  Enter confirm  Esc cancel"))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")

	if len(m.files) == 0 {
		sb.WriteString(dimStyle.Render("  no markdown files found"))
		return sb.String()
	}

	rows := m.height - 4
	if rows < 3 {
		rows = 3
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	for i := start; i < len(m.files) && i < start+rows; i++ {
		rel := m.files[i]
		check := "[ ]"
		if m.selected[rel] {
			check = brightStyle.Render("[x]")
		}
		label := rel
		if i == m.cursor {
			sb.WriteString(brightStyle.Render("▸ ") + check + " " + brightStyle.Render(label))
		} else {
			sb.WriteString("  " + check + " " + dimStyle.Render(label))
		}
		sb.WriteString("\n")
	}
	if len(m.files) > rows {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d", m.cursor+1, len(m.files))))
	}
	return sb.String()
}

// loadAttachments reads selected relative paths into chatAttachment values,
// truncating oversized files to keep the prompt bounded.
func loadAttachments(root string, rels []string) []chatAttachment {
	var out []chatAttachment
	for _, rel := range rels {
		// rel originates from findMarkdownFiles (paths discovered under root);
		// reject any traversal attempt defensively before reading.
		full := filepath.Join(root, rel)
		if !strings.HasPrefix(full, filepath.Clean(root)+string(os.PathSeparator)) {
			continue
		}
		data, err := os.ReadFile(full) // #nosec G304 -- path constrained to root above
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > maxAttachBytes {
			content = content[:maxAttachBytes] + "\n…(truncated)"
		}
		out = append(out, chatAttachment{rel: rel, content: content})
	}
	return out
}

// attachmentBlock renders attached files as a system-prompt context section.
func attachmentBlock(attached []chatAttachment) string {
	if len(attached) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n# Attached context files\n")
	sb.WriteString("The user attached the following files for reference:\n")
	for _, a := range attached {
		fmt.Fprintf(&sb, "\n## %s\n\n", a.rel)
		sb.WriteString(a.content)
		sb.WriteString("\n")
	}
	return sb.String()
}
