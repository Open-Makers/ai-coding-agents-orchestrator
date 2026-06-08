package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

// maxPreviewBytes caps the size of a previewed file to keep highlighting and
// rendering responsive.
const maxPreviewBytes = 256 * 1024

// FilePreviewModel renders a syntax-highlighted, scrollable view of a single
// file. It replaces the active agent panel while the user browses the project
// tree in the System Monitor.
type FilePreviewModel struct {
	root    string
	path    string // repo-relative path of the previewed file ("" = none)
	vp      viewport.Model
	width   int
	height  int
	errMsg  string
	focused bool // true when arrows scroll the file (vs. navigate the tree)
}

// NewFilePreview creates an empty preview rooted at root.
func NewFilePreview(root string, w, h int) FilePreviewModel {
	m := FilePreviewModel{root: root, width: w, height: h}
	m.vp = viewport.New(w-2, previewBodyHeight(h))
	return m
}

func previewBodyHeight(h int) int {
	body := h - 2 // title + hint
	if body < 1 {
		body = 1
	}
	return body
}

// SetSize resizes the preview and re-renders the current file only when the
// width changes (re-highlighting on every frame would be wasteful).
func (m *FilePreviewModel) SetSize(w, h int) {
	widthChanged := w != m.width
	m.width = w
	m.height = h
	m.vp.Width = w - 2
	m.vp.Height = previewBodyHeight(h)
	if widthChanged && m.path != "" {
		m.load(m.path)
	}
}

// Show loads relPath into the preview. Directories and unreadable files set an
// informational message instead of content.
func (m *FilePreviewModel) Show(relPath string, isDir bool) {
	if isDir {
		m.path = relPath
		m.errMsg = ""
		m.vp.SetContent(lipgloss.NewStyle().Foreground(crt.dim).
			Render("  " + relPath + "/  — select a file to preview"))
		m.vp.GotoTop()
		return
	}
	m.load(relPath)
}

func (m *FilePreviewModel) load(relPath string) {
	m.path = relPath
	m.errMsg = ""

	data, err := safefile.ReadFile(m.root, relPath)
	if err != nil {
		m.errMsg = err.Error()
		m.vp.SetContent(lipgloss.NewStyle().Foreground(crt.warn).
			Render("  cannot read file: " + err.Error()))
		m.vp.GotoTop()
		return
	}
	if len(data) > maxPreviewBytes {
		data = data[:maxPreviewBytes]
	}
	if isBinaryPreviewContent(data) {
		m.vp.SetContent(lipgloss.NewStyle().Foreground(crt.dim).
			Render("  (binary file — no preview)"))
		m.vp.GotoTop()
		return
	}

	lang := languageForPath(relPath)
	h := newHighlighter(lang)
	lines := strings.Split(strings.ReplaceAll(string(data), "\t", "    "), "\n")
	// Wrap each source line in a fence so the highlighter colours the whole
	// file, then strip the synthetic fence lines from the result.
	fenced := append([]string{"```" + lang}, lines...)
	fenced = append(fenced, "```")
	highlighted := h.highlightLines(fenced)
	if len(highlighted) >= 2 {
		highlighted = highlighted[1 : len(highlighted)-1]
	}

	numbered := make([]string, len(highlighted))
	gutter := lipgloss.NewStyle().Foreground(crt.dim)
	for i, line := range highlighted {
		numbered[i] = gutter.Render(fmt.Sprintf("%4d ", i+1)) + line
	}

	m.vp.SetContent(strings.Join(numbered, "\n"))
	m.vp.GotoTop()
}

// ScrollUp / ScrollDown move the preview body without affecting tree selection.
func (m *FilePreviewModel) ScrollUp()   { m.vp.ScrollUp(3) }
func (m *FilePreviewModel) ScrollDown() { m.vp.ScrollDown(3) }

// PageUp / PageDown scroll by a full viewport page.
func (m *FilePreviewModel) PageUp()   { m.vp.ScrollUp(m.vp.Height) }
func (m *FilePreviewModel) PageDown() { m.vp.ScrollDown(m.vp.Height) }

// GotoTop / GotoBottom jump to the start/end of the file.
func (m *FilePreviewModel) GotoTop()    { m.vp.GotoTop() }
func (m *FilePreviewModel) GotoBottom() { m.vp.GotoBottom() }

// SetFocused toggles the preview-scroll hint. When focused, arrow keys scroll
// the file; otherwise they navigate the tree.
func (m *FilePreviewModel) SetFocused(b bool) { m.focused = b }

func (m FilePreviewModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)

	title := "File Preview"
	if m.path != "" {
		title += "  " + m.path
	}

	var hint string
	if m.focused {
		hint = dimStyle.Render("↑↓/jk scroll · PgUp/PgDn page · g/G top/bottom · Esc back to file list")
	} else {
		hint = dimStyle.Render("↑↓ select file · Enter open & scroll · Esc back to agent output")
	}

	return strings.Join([]string{
		titleStyle.Render(title),
		m.vp.View(),
		hint,
	}, "\n")
}

// languageForPath maps a file extension to a highlighter language hint.
func languageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx", ".jsx":
		return "jsx"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".hpp":
		return "cpp"
	case ".sh", ".bash":
		return "bash"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".md", ".markdown":
		return "markdown"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	default:
		return "text"
	}
}

// isBinaryPreviewContent reports whether data looks like binary (contains a NUL
// byte in the first chunk).
func isBinaryPreviewContent(data []byte) bool {
	n := len(data)
	if n > 1024 {
		n = 1024
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
