package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// chatLine is one entry in the chat history view.
type chatLine struct {
	role    string // "user" | "assistant"
	content string
}

// ReviseFunc is called when the PM decides to revise an artifact.
type ReviseFunc func(artifact, feedback string) error

// ChatModel is a live chat panel overlay backed by an LLMRunner.
type ChatModel struct {
	llm          runner.LLMRunner
	history      []runner.ConvMessage
	lines        []chatLine
	input        string
	vp           viewport.Model
	waiting      bool
	ctx          context.Context
	cancelFn     context.CancelFunc
	width        int
	height       int
	systemPrompt string
	reviseFn     ReviseFunc // called when PM decides to revise an artifact
	revising     bool       // true while a revision is in progress
}

// NewChat creates a ChatModel with the given LLMRunner.
// systemContext is prepended to every request as the system prompt.
func NewChat(llm runner.LLMRunner, systemContext string) ChatModel {
	vp := viewport.New(76, 18)
	ctx, cancel := context.WithCancel(context.Background())
	return ChatModel{
		llm:          llm,
		vp:           vp,
		ctx:          ctx,
		cancelFn:     cancel,
		width:        80,
		height:       24,
		systemPrompt: systemContext,
	}
}

// WithReviseFn sets a callback for artifact revision (PM chat mode).
func (m ChatModel) WithReviseFn(fn ReviseFunc) ChatModel {
	m.reviseFn = fn
	return m
}

func (m ChatModel) Init() tea.Cmd { return nil }

// chatSendCmd sends the current conversation to the LLM and streams tokens.
func chatSendCmd(llm runner.LLMRunner, ctx context.Context, systemPrompt string, history []runner.ConvMessage) tea.Cmd {
	return func() tea.Msg {
		ch, err := llm.Complete(ctx, runner.CompletionRequest{
			SystemPrompt: systemPrompt,
			Messages:     history,
		})
		if err != nil {
			return ChatTokenMsg{Text: "error: " + err.Error(), Done: true}
		}
		// Return the channel wrapped so TUI can drain it.
		return chatStreamStartMsg{ch: ch}
	}
}

// chatStreamStartMsg carries the token channel to the Update loop.
type chatStreamStartMsg struct {
	ch <-chan runner.Token
}

// chatDrainCmd reads one token from the channel.
func chatDrainCmd(ch <-chan runner.Token) tea.Cmd {
	return func() tea.Msg {
		tok, ok := <-ch
		if !ok {
			return ChatTokenMsg{Done: true}
		}
		if tok.Error != nil {
			return ChatTokenMsg{Text: "error: " + tok.Error.Error(), Done: true}
		}
		return chatRawToken{text: tok.Text, done: tok.Done, ch: ch}
	}
}

type chatRawToken struct {
	text string
	done bool
	ch   <-chan runner.Token
}

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.waiting {
			// Only allow cancel while waiting.
			if msg.String() == "ctrl+c" || msg.String() == "esc" {
				m.cancelFn()
				m.waiting = false
				// Recreate context for next use.
				m.ctx, m.cancelFn = context.WithCancel(context.Background())
				m.refreshViewport()
			}
			return m, nil
		}

		switch msg.String() {
		case "esc":
			m.cancelFn()
			return m, func() tea.Msg { return ChatClosedMsg{} }
		case "enter":
			if m.input == "" {
				break
			}
			userMsg := m.input
			m.input = ""
			m.history = append(m.history, runner.ConvMessage{Role: "user", Content: userMsg})
			m.lines = append(m.lines, chatLine{role: "user", content: userMsg})
			m.waiting = true
			m.refreshViewport()
			return m, chatSendCmd(m.llm, m.ctx, m.systemPrompt, m.history)
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		default:
			if len(msg.String()) == 1 {
				m.input += msg.String()
			}
		}

	case chatStreamStartMsg:
		// Start draining.
		m.lines = append(m.lines, chatLine{role: "assistant", content: ""})
		return m, chatDrainCmd(msg.ch)

	case chatRawToken:
		if len(m.lines) > 0 {
			last := &m.lines[len(m.lines)-1]
			if last.role == "assistant" {
				last.content += msg.text
			}
		}
		m.refreshViewport()
		if msg.done {
			// Commit assistant message to history.
			if len(m.lines) > 0 {
				last := m.lines[len(m.lines)-1]
				if last.role == "assistant" {
					m.history = append(m.history, runner.ConvMessage{
						Role:    "assistant",
						Content: last.content,
					})
					// Check for revision directive from PM.
					if artifact, feedback := parseReviseDirective(last.content); artifact != "" && m.reviseFn != nil {
						m.revising = true
						m.refreshViewport()
						return m, reviseArtifactCmd(m.reviseFn, artifact, feedback)
					}
				}
			}
			m.waiting = false
			return m, nil
		}
		return m, chatDrainCmd(msg.ch)

	case chatRevisionDoneMsg:
		m.revising = false
		if msg.err != nil {
			m.lines = append(m.lines, chatLine{
				role:    "assistant",
				content: "⚠ revision failed: " + msg.err.Error(),
			})
		} else {
			m.lines = append(m.lines, chatLine{
				role:    "assistant",
				content: "✓ revised " + msg.artifact + " — close chat (Esc) and re-approve the artifact",
			})
		}
		m.waiting = false
		m.refreshViewport()
		return m, nil
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *ChatModel) refreshViewport() {
	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	var sb strings.Builder
	for _, line := range m.lines {
		switch line.role {
		case "user":
			sb.WriteString(userStyle.Render("you › ") + line.content + "\n\n")
		case "assistant":
			prefix := dimStyle.Render("ai › ")
			sb.WriteString(prefix + assistantStyle.Render(line.content) + "\n\n")
		}
	}
	m.vp.SetContent(sb.String())
	m.vp.GotoBottom()
}

func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.vp.Width = w - 4
	m.vp.Height = h - 6
	if m.vp.Height < 3 {
		m.vp.Height = 3
	}
	m.refreshViewport()
}

func (m ChatModel) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	sep := strings.Repeat("─", m.width)

	title := "Chat with PM"
	if m.reviseFn == nil {
		title = "Chat"
	}

	var statusLine string
	if m.revising {
		statusLine = dimStyle.Render("  revising artifact…")
	} else if m.waiting {
		statusLine = dimStyle.Render("  waiting… (Esc cancel)")
	} else {
		statusLine = inputStyle.Render("› ") + m.input + "█"
	}

	return strings.Join([]string{
		titleStyle.Render(title) + "  " + dimStyle.Render("Esc close  Enter send"),
		sep,
		m.vp.View(),
		sep,
		statusLine,
	}, "\n")
}
