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
// repository context to the PM chat. Beyond the discovered files, the user can
// type an arbitrary path (Ctrl+O) to attach any .md file, including ones
// outside the repository.
type mdPicker struct {
	root     string
	files    []string        // relative paths (discovered) + manually-added paths
	selected map[string]bool // keyed by path as listed in files
	cursor   int
	width    int
	height   int

	entering    bool   // true while typing a manual path
	entry       string // the manual path being typed
	entryCursor int    // rune index in entry
	entryErr    string // last manual-entry error (e.g. file not found)
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

	// Manual path entry mode: type any .md path (relative to root or absolute).
	if m.entering {
		switch key.String() {
		case "esc":
			m.entering = false
			m.entry = ""
			m.entryCursor = 0
			m.entryErr = ""
		case "enter":
			m = m.commitManualEntry()
		case "left":
			if m.entryCursor > 0 {
				m.entryCursor--
			}
		case "right":
			if m.entryCursor < runeLen(m.entry) {
				m.entryCursor++
			}
		case "home":
			m.entryCursor = 0
		case "end":
			m.entryCursor = runeLen(m.entry)
		case "backspace":
			m.entry, m.entryCursor = runeDeleteBefore(m.entry, m.entryCursor)
		case "ctrl+u":
			m.entry = string([]rune(m.entry)[m.entryCursor:])
			m.entryCursor = 0
		default:
			if len(key.Runes) > 0 {
				m.entry, m.entryCursor = runeInsert(m.entry, m.entryCursor, key.Runes)
			}
		}
		return m, nil
	}

	switch key.String() {
	case "esc":
		return m, func() tea.Msg { return mdPickerDoneMsg{cancel: true} }
	case "ctrl+o":
		m.entering = true
		m.entryErr = ""
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

// commitManualEntry validates the typed path, adds it to the file list, selects
// it, and exits entry mode. On error it keeps entry mode open with a message.
func (m mdPicker) commitManualEntry() mdPicker {
	p := strings.TrimSpace(m.entry)
	if p == "" {
		m.entering = false
		return m
	}
	// Expand a leading ~ to the user's home directory for convenience.
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	full := p
	if !filepath.IsAbs(p) {
		full = filepath.Join(m.root, p)
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		m.entryErr = "not a readable file: " + p
		return m
	}
	// Store the listing key: a path relative to root when possible (nicer
	// display and consistent with discovered entries), otherwise the absolute
	// path so files outside the repo still work.
	key := full
	if rel, rerr := filepath.Rel(m.root, full); rerr == nil && !strings.HasPrefix(rel, "..") {
		key = rel
	}
	if !sliceContains(m.files, key) {
		m.files = append([]string{key}, m.files...)
	}
	m.selected[key] = true
	m.cursor = sliceIndexOf(m.files, key)
	m.entering = false
	m.entry = ""
	m.entryCursor = 0
	m.entryErr = ""
	return m
}

func sliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func sliceIndexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return 0
}

func (m mdPicker) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	brightStyle := lipgloss.NewStyle().Foreground(crt.bright)

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Attach .md context"))
	sb.WriteString("  " + dimStyle.Render("Space toggle  Enter confirm  Ctrl+O type path  Esc cancel"))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")

	// Manual path entry line.
	if m.entering {
		sb.WriteString(brightStyle.Render("path › ") + renderInputWithCursor(m.entry, m.entryCursor))
		sb.WriteString("\n")
		if m.entryErr != "" {
			sb.WriteString(dimStyle.Render("  " + m.entryErr))
			sb.WriteString("\n")
		} else {
			sb.WriteString(dimStyle.Render("  type any .md path (relative or absolute), Enter to add, Esc to cancel"))
			sb.WriteString("\n")
		}
	}

	if len(m.files) == 0 {
		if !m.entering {
			sb.WriteString(dimStyle.Render("  no markdown files found — press Ctrl+O to type a path"))
		}
		return sb.String()
	}

	rows := m.height - 4
	if m.entering {
		rows -= 2
	}
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

// loadAttachments reads selected paths into chatAttachment values, truncating
// oversized files to keep the prompt bounded. A path is either relative to root
// (discovered files) or absolute (manually entered, possibly outside the repo).
func loadAttachments(root string, paths []string) []chatAttachment {
	var out []chatAttachment
	for _, p := range paths {
		full := p
		if !filepath.IsAbs(p) {
			full = filepath.Join(root, p)
			// Relative paths must stay within root (defensive: reject traversal).
			if !strings.HasPrefix(full, filepath.Clean(root)+string(os.PathSeparator)) {
				continue
			}
		}
		data, err := os.ReadFile(full) // #nosec G304 -- user explicitly selected this path
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > maxAttachBytes {
			content = content[:maxAttachBytes] + "\n…(truncated)"
		}
		out = append(out, chatAttachment{rel: p, content: content})
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
