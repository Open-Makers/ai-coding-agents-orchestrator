package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/gitclient"
)

type gitFocus int

const (
	gitFocusFiles gitFocus = iota
	gitFocusDiff
	gitFocusCommit
)

// GitPanelModel is a lazygit-inspired git panel overlay.
type GitPanelModel struct {
	gc        *gitclient.GitClient
	files     []gitclient.FileStatus
	cursor    int
	focus     gitFocus
	diff      viewport.Model
	commitMsg string
	statusMsg string
	err       string
	width     int
	height    int
}

// NewGitPanel creates a GitPanelModel for the given repo root.
func NewGitPanel(root string) (GitPanelModel, error) {
	gc := gitclient.New(root)
	files, err := gc.Status()
	if err != nil {
		return GitPanelModel{}, fmt.Errorf("git status: %w", err)
	}

	vp := viewport.New(60, 20)

	m := GitPanelModel{
		gc:     gc,
		files:  files,
		diff:   vp,
		width:  80,
		height: 24,
	}
	m.loadDiff()
	return m, nil
}

func (m *GitPanelModel) loadDiff() {
	if len(m.files) == 0 {
		m.diff.SetContent("(no changes)")
		return
	}
	f := m.files[m.cursor]
	var diff string
	var err error
	if f.Staged {
		diff, err = m.gc.StagedDiff(f.Path)
	} else {
		diff, err = m.gc.Diff(f.Path)
	}
	if err != nil {
		diff = fmt.Sprintf("error: %v", err)
	}
	if diff == "" {
		diff = "(no diff)"
	}
	m.diff.SetContent(diff)
	m.diff.GotoTop()
}

func (m GitPanelModel) Init() tea.Cmd { return nil }

func (m GitPanelModel) Update(msg tea.Msg) (GitPanelModel, tea.Cmd) {
	switch msg := msg.(type) {
	case GitRefreshMsg:
		files, err := m.gc.Status()
		if err != nil {
			m.err = err.Error()
		} else {
			m.err = ""
			m.files = files
			if m.cursor >= len(m.files) && len(m.files) > 0 {
				m.cursor = len(m.files) - 1
			}
			m.loadDiff()
		}
		return m, nil

	case tea.KeyMsg:
		switch m.focus {
		case gitFocusFiles:
			return m.updateFiles(msg)
		case gitFocusDiff:
			return m.updateDiff(msg)
		case gitFocusCommit:
			return m.updateCommit(msg)
		}
	}

	return m, nil
}

func (m GitPanelModel) updateFiles(msg tea.KeyMsg) (GitPanelModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return m, func() tea.Msg { return GitPanelClosedMsg{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.loadDiff()
		}
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
			m.loadDiff()
		}
	case " ":
		if len(m.files) == 0 {
			break
		}
		f := m.files[m.cursor]
		var err error
		if f.Staged {
			err = m.gc.Unstage(f.Path)
		} else {
			err = m.gc.Stage(f.Path)
		}
		if err != nil {
			m.statusMsg = "error: " + err.Error()
		} else {
			m.statusMsg = ""
		}
		return m, func() tea.Msg { return GitRefreshMsg{} }
	case "r":
		if len(m.files) == 0 {
			break
		}
		f := m.files[m.cursor]
		if err := m.gc.ResetFile(f.Path); err != nil {
			m.statusMsg = "error: " + err.Error()
		} else {
			m.statusMsg = "reset: " + f.Path
		}
		return m, func() tea.Msg { return GitRefreshMsg{} }
	case "tab":
		m.focus = gitFocusDiff
	case "c":
		m.focus = gitFocusCommit
		m.commitMsg = ""
	case "enter":
		m.focus = gitFocusDiff
	}
	return m, nil
}

func (m GitPanelModel) updateDiff(msg tea.KeyMsg) (GitPanelModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "tab":
		m.focus = gitFocusFiles
	default:
		var cmd tea.Cmd
		m.diff, cmd = m.diff.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m GitPanelModel) updateCommit(msg tea.KeyMsg) (GitPanelModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.focus = gitFocusFiles
	case "enter":
		if m.commitMsg == "" {
			m.statusMsg = "commit message required"
			break
		}
		if err := m.gc.Commit(m.commitMsg); err != nil {
			m.statusMsg = "error: " + err.Error()
		} else {
			m.statusMsg = "committed: " + m.commitMsg
			m.commitMsg = ""
			m.focus = gitFocusFiles
		}
		return m, func() tea.Msg { return GitRefreshMsg{} }
	case "backspace":
		if len(m.commitMsg) > 0 {
			m.commitMsg = m.commitMsg[:len(m.commitMsg)-1]
		}
	default:
		// Only printable single chars
		if len(msg.String()) == 1 {
			m.commitMsg += msg.String()
		}
	}
	return m, nil
}

func (m *GitPanelModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	diffW := w*2/3 - 2
	diffH := h - 7
	if diffH < 3 {
		diffH = 3
	}
	m.diff.Width = diffW
	m.diff.Height = diffH
}

func (m GitPanelModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	stagedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	unstagedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

	fileW := m.width / 3
	diffW := m.width - fileW - 3

	// File list panel
	var filesSB strings.Builder
	var label string
	if m.focus == gitFocusFiles {
		label = activeStyle.Render("Files")
	} else {
		label = titleStyle.Render("Files")
	}
	filesSB.WriteString(label + "\n")

	if len(m.files) == 0 {
		filesSB.WriteString(dimStyle.Render("  nothing to commit") + "\n")
	}
	for i, f := range m.files {
		code := f.Code
		name := f.Path
		var line string
		if f.Staged {
			line = stagedStyle.Render(code + " " + name)
		} else {
			line = unstagedStyle.Render(code + " " + name)
		}
		if i == m.cursor && m.focus == gitFocusFiles {
			line = "► " + line
		} else {
			line = "  " + line
		}
		filesSB.WriteString(line + "\n")
	}

	filesPanel := borderStyle.Width(fileW).Render(filesSB.String())

	// Diff panel
	var diffLabel string
	if m.focus == gitFocusDiff {
		diffLabel = activeStyle.Render("Diff (↑↓ scroll, Tab/Esc back)")
	} else {
		diffLabel = titleStyle.Render("Diff")
	}
	m.diff.Width = diffW - 2
	diffContent := diffLabel + "\n" + m.diff.View()
	diffPanel := borderStyle.Width(diffW).Render(diffContent)

	top := lipgloss.JoinHorizontal(lipgloss.Top, filesPanel, diffPanel)

	// Status / commit bar
	var bottom string
	if m.focus == gitFocusCommit {
		bottom = activeStyle.Render("Commit message: ") + m.commitMsg + "█"
	} else {
		hints := "Space stage/unstage  r reset  c commit  Tab diff  Esc/q close"
		bottom = dimStyle.Render(hints)
	}
	if m.statusMsg != "" {
		bottom = bottom + "  " + dimStyle.Render(m.statusMsg)
	}
	if m.err != "" {
		bottom = errorStyle.Render("error: " + m.err)
	}

	return strings.Join([]string{
		titleStyle.Render("Git") + " " + dimStyle.Render("(lazygit-lite)"),
		top,
		bottom,
	}, "\n")
}
