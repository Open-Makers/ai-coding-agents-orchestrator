package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SendHumanReplyFunc is called when the user sends a reply during PM negotiation.
type SendHumanReplyFunc func(msg string)

const negotiateAcceptNowMessage = "Tak, to się zgadza. Nie zadawaj więcej pytań i przejdź od razu do TASKSPEC na podstawie obecnych informacji oraz kontekstu projektu."

// NegotiateModel is the PM conversation overlay for requirements gathering.
type NegotiateModel struct {
	lines   []chatLine
	input   string
	cursor  int // rune index in input
	vp      viewport.Model
	sendFn  SendHumanReplyFunc
	waiting bool // true while waiting for PM response
	width   int
	height  int
}

// NewNegotiate creates a NegotiateModel with the given reply callback.
func NewNegotiate(sendFn SendHumanReplyFunc) NegotiateModel {
	vp := viewport.New(76, 18)
	return NegotiateModel{
		vp:     vp,
		sendFn: sendFn,
		width:  80,
		height: 24,
	}
}

func (m NegotiateModel) Init() tea.Cmd { return nil }

// AddPMMessage appends a PM conversation message to the negotiate model.
func (m *NegotiateModel) AddPMMessage(content string) {
	m.lines = append(m.lines, chatLine{role: "assistant", content: content})
	m.waiting = false
	m.refreshViewport()
}

// SeedContext shows initial context (e.g. the requirements the user submitted)
// at the top of the conversation, before any PM reply, so the screen is never
// blank and the user can review what they're refining.
func (m *NegotiateModel) SeedContext(content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	m.lines = append(m.lines, chatLine{role: "context", content: content})
	m.refreshViewport()
}

// SetReady stops the waiting indicator without appending a message. Used when
// the PM returns no question, so the user can refine or accept (Ctrl+A).
func (m *NegotiateModel) SetReady() {
	m.waiting = false
	m.refreshViewport()
}

func (m NegotiateModel) Update(msg tea.Msg) (NegotiateModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.waiting {
			return m, nil
		}

		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return NegotiateClosedMsg{} }
		case "ctrl+c":
			// Clear the currently typed text (like Claude/Codex), without
			// closing the conversation.
			m.input = ""
			m.cursor = 0
		case "ctrl+u":
			// Clear from the start of the line to the cursor.
			m.input = string([]rune(m.input)[m.cursor:])
			m.cursor = 0
		case "ctrl+a":
			userMsg := negotiateAcceptNowMessage
			m.input = ""
			m.cursor = 0
			m.lines = append(m.lines, chatLine{role: "user", content: userMsg})
			m.waiting = true
			m.refreshViewport()
			if m.sendFn != nil {
				m.sendFn(userMsg)
			}
		case "enter":
			if m.input == "" {
				break
			}
			userMsg := m.input
			m.input = ""
			m.cursor = 0
			m.lines = append(m.lines, chatLine{role: "user", content: userMsg})
			m.waiting = true
			m.refreshViewport()
			if m.sendFn != nil {
				m.sendFn(userMsg)
			}
		case "left":
			if m.cursor > 0 {
				m.cursor--
			}
		case "right":
			if m.cursor < runeLen(m.input) {
				m.cursor++
			}
		case "home":
			m.cursor = 0
		case "end", "ctrl+e":
			m.cursor = runeLen(m.input)
		case "backspace":
			m.input, m.cursor = runeDeleteBefore(m.input, m.cursor)
		case "delete":
			m.input, m.cursor = runeDeleteAt(m.input, m.cursor)
		default:
			if len(msg.Runes) > 0 {
				m.input, m.cursor = runeInsert(m.input, m.cursor, msg.Runes)
			}
		}
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *NegotiateModel) refreshViewport() {
	userStyle := lipgloss.NewStyle().Foreground(crt.bright).Bold(true)
	pmStyle := lipgloss.NewStyle().Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)

	var sb strings.Builder
	for _, line := range m.lines {
		switch line.role {
		case "context":
			sb.WriteString(renderWrappedChatLine("requirements › ", line.content, dimStyle, dimStyle, m.vp.Width))
			sb.WriteString("\n\n")
		case "user":
			sb.WriteString(renderWrappedChatLine("you › ", line.content, userStyle, lipgloss.NewStyle(), m.vp.Width))
			sb.WriteString("\n\n")
		case "assistant":
			sb.WriteString(renderWrappedChatLine("pm › ", line.content, dimStyle, pmStyle, m.vp.Width))
			sb.WriteString("\n\n")
		}
	}
	m.vp.SetContent(sb.String())
	m.vp.GotoBottom()
}

func (m *NegotiateModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.vp.Width = w - 4
	m.vp.Height = h - 6
	if m.vp.Height < 3 {
		m.vp.Height = 3
	}
	m.refreshViewport()
}

func (m NegotiateModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(crt.primary)
	dimStyle := lipgloss.NewStyle().Foreground(crt.dim)
	inputStyle := lipgloss.NewStyle().Foreground(crt.bright)

	sep := strings.Repeat("─", m.width)

	var statusLine string
	if m.waiting {
		statusLine = dimStyle.Render("  PM is thinking…")
	} else {
		statusLine = inputStyle.Render("› ") + renderInputWithCursor(m.input, m.cursor)
	}

	return strings.Join([]string{
		titleStyle.Render("PM Requirements Conversation") + "  " + dimStyle.Render("Esc close  Enter send  Ctrl+A accept now  Ctrl+C clear"),
		sep,
		m.vp.View(),
		sep,
		statusLine,
	}, "\n")
}

// NegotiateClosedMsg is sent when the user closes the negotiate overlay.
type NegotiateClosedMsg struct{}

func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	if size <= 0 {
		return ""
	}
	return s[:len(s)-size]
}

func renderWrappedChatLine(prefix, content string, prefixStyle, contentStyle lipgloss.Style, width int) string {
	maxContentWidth := width - len(prefix)
	if maxContentWidth < 8 {
		maxContentWidth = 8
	}

	wrapped := wrapLine(content, maxContentWidth)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	indent := strings.Repeat(" ", len(prefix))
	var lines []string
	for i, part := range wrapped {
		if i == 0 {
			lines = append(lines, prefixStyle.Render(prefix)+contentStyle.Render(part))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s%s", indent, contentStyle.Render(part)))
	}
	return strings.Join(lines, "\n")
}
