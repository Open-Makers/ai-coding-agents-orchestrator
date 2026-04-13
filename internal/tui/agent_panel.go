package tui

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/logging"
)

const maxPanelLines = 500

// AgentPanelModel displays the status and scrollable output of one agent.
type AgentPanelModel struct {
	role        bus.AgentRole
	state       AgentState
	lines       []string // completed lines, capped at maxPanelLines
	currentLine string   // tokens accumulate here until \n

	vp       viewport.Model // scrollable viewport for output
	pinToEnd bool           // auto-scroll to bottom on new content

	spinner   spinner.Model
	highlight *highlighter // syntax highlighter for code blocks
	width     int
	height    int
	log       *slog.Logger
}

func NewAgentPanel(role bus.AgentRole) AgentPanelModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleRunning
	vp := viewport.New(28, 4)
	vp.SetContent("")
	return AgentPanelModel{
		role:     role,
		state:    AgentWaiting,
		spinner:  s,
		vp:       vp,
		pinToEnd: true,
		width:    30,
		height:   6,
		log:      logging.ForComponent("agent_panel").With(slog.String("role", string(role))),
	}
}

func (m AgentPanelModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m AgentPanelModel) Update(msg tea.Msg) (AgentPanelModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case AgentEventMsg:
		if msg.Role != m.role {
			return m, nil
		}
		if msg.State != "" {
			prevState := m.state
			m.state = msg.State
			m.log.Debug("state transition",
				slog.String("from", string(prevState)),
				slog.String("to", string(m.state)),
			)
		}
		if msg.Text != "" {
			m.log.Debug("received text",
				slog.Int("text_len", len(msg.Text)),
				slog.Int("buffered_lines", len(m.lines)),
			)
			m.addLine(msg.Text)
			m.syncViewport()
		}
		if m.state == AgentRunning || m.state == AgentFixing {
			cmds = append(cmds, m.spinner.Tick)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.pinToEnd = false
			m.vp.ScrollUp(1)
		case "down", "j":
			m.vp.ScrollDown(1)
			m.pinToEnd = m.vp.AtBottom()
		case "pgup", "b":
			m.pinToEnd = false
			m.vp.HalfPageUp()
		case "pgdown", "f":
			m.vp.HalfPageDown()
			m.pinToEnd = m.vp.AtBottom()
		case "home", "g":
			m.pinToEnd = false
			m.vp.GotoTop()
		case "end", "G":
			m.vp.GotoBottom()
			m.pinToEnd = true
		}
		return m, nil

	case spinner.TickMsg:
		if m.state != AgentRunning && m.state != AgentFixing {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *AgentPanelModel) addLine(text string) {
	for _, ch := range text {
		if ch == '\n' {
			m.lines = append(m.lines, m.currentLine)
			m.currentLine = ""
		} else {
			m.currentLine += string(ch)
		}
	}
	if len(m.lines) > maxPanelLines {
		trimmed := len(m.lines) - maxPanelLines
		m.lines = m.lines[trimmed:]
		m.log.Debug("buffer trimmed", slog.Int("dropped_lines", trimmed))
	}
}

func (m AgentPanelModel) View() string {
	contentW := m.width - 2 // border left + right
	if contentW < 2 {
		contentW = 2
	}

	color := roleColor(string(m.role))
	bSt := lipgloss.NewStyle().Foreground(color)

	header := m.renderHeader(contentW)
	hRule := strings.Repeat("─", contentW)

	var sb strings.Builder
	sb.WriteString(bSt.Render("╭"+hRule+"╮") + "\n")

	// Header row.
	vis := lipgloss.Width(header)
	pad := contentW - vis
	if pad < 0 {
		pad = 0
	}
	sb.WriteString(bSt.Render("│") + header + strings.Repeat(" ", pad) + bSt.Render("│") + "\n")

	// Viewport rows.
	vpLines := strings.Split(m.vp.View(), "\n")
	for _, raw := range vpLines {
		vis := lipgloss.Width(raw)
		pad := contentW - vis
		if pad < 0 {
			pad = 0
		}
		sb.WriteString(bSt.Render("│") + raw + strings.Repeat(" ", pad) + bSt.Render("│") + "\n")
	}

	// Scroll indicator.
	scrollInfo := m.scrollIndicator()
	vis = lipgloss.Width(scrollInfo)
	bottomRule := hRule
	if vis > 0 && vis+2 < contentW {
		bottomRule = hRule[:contentW-vis-1] + scrollInfo + "─"
	}
	sb.WriteString(bSt.Render("╰" + bottomRule + "╯"))
	return sb.String()
}

// renderHeader returns the formatted header line with role name and status.
func (m AgentPanelModel) renderHeader(_ int) string {
	roleStyle := lipgloss.NewStyle().Foreground(roleColor(string(m.role))).Bold(true)
	header := roleStyle.Render(fmt.Sprintf("[%s]", strings.ToUpper(string(m.role))))

	var statusIcon string
	switch m.state {
	case AgentWaiting:
		statusIcon = styleWaiting.Render("○ WAITING")
	case AgentRunning:
		statusIcon = m.spinner.View() + styleRunning.Render(" RUNNING")
	case AgentFixing:
		statusIcon = m.spinner.View() + styleFixing.Render(" FIXING")
	case AgentDone:
		statusIcon = styleDone.Render("✓ DONE")
	case AgentError:
		statusIcon = styleError.Render("✗ ERROR")
	case AgentGate:
		statusIcon = styleGate.Render("⏸ GATE")
	}

	return header + "  " + statusIcon
}

// scrollIndicator returns a short string showing scroll position.
func (m AgentPanelModel) scrollIndicator() string {
	total := m.vp.TotalLineCount()
	visible := m.vp.VisibleLineCount()
	if total <= visible {
		return ""
	}
	pct := m.vp.ScrollPercent() * 100
	indicator := fmt.Sprintf(" %d%% ", int(pct))
	return lipgloss.NewStyle().Foreground(crt.dim).Render(indicator)
}

// syncViewport updates the viewport content from the line buffer.
func (m *AgentPanelModel) syncViewport() {
	src := m.lines
	if m.currentLine != "" {
		src = append(append([]string{}, m.lines...), m.currentLine)
	}
	// Apply syntax highlighting to code blocks.
	if m.highlight != nil {
		src = m.highlight.highlightLines(src)
	}
	m.vp.SetContent(strings.Join(src, "\n"))
	if m.pinToEnd {
		m.vp.GotoBottom()
	}
}

// SetLanguage configures syntax highlighting for the given programming language.
func (m *AgentPanelModel) SetLanguage(lang string) {
	if lang != "" {
		m.highlight = newHighlighter(lang)
	}
}

// SetSize updates panel dimensions.
func (m *AgentPanelModel) SetSize(w, h int) {
	m.log.Debug("resize", slog.Int("width", w), slog.Int("height", h))
	m.width = w
	m.height = h
	contentW := w - 2
	if contentW < 2 {
		contentW = 2
	}
	contentH := h - 3 // top border + header + bottom border
	if contentH < 1 {
		contentH = 1
	}
	m.vp.Width = contentW
	m.vp.Height = contentH
	m.syncViewport()
}
