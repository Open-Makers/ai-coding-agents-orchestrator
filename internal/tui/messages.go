package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// BusMessageMsg wraps a bus.Message as a tea.Msg.
type BusMessageMsg struct {
	Msg bus.Message
}

// AgentState represents the display state of an agent panel.
type AgentState string

const (
	AgentWaiting AgentState = "waiting"
	AgentRunning AgentState = "running"
	AgentDone    AgentState = "done"
	AgentError   AgentState = "error"
	AgentGate    AgentState = "gate"
)

// AgentEventMsg signals a state change or output for a specific agent.
type AgentEventMsg struct {
	Role  bus.AgentRole
	Text  string
	State AgentState
}

// Requirements picker/editor messages.
type RequirementsSavedMsg struct{ Path string }
type EditorCancelledMsg struct{}
type PickerSelectedMsg struct {
	Path  string
	IsNew bool
}
type FileSelectedMsg struct{ Path string }

// Git panel messages.
type FileRejectedMsg struct{ Path string }
type GitPanelClosedMsg struct{}
type GitRefreshMsg struct{}

// Chat messages.
type ChatTokenMsg struct {
	Text string
	Done bool
}
type ChatClosedMsg struct{}

// waitForBusEvent is a tea.Cmd that reads one message from the bus channel.
func waitForBusEvent(ch <-chan bus.Message) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return BusMessageMsg{Msg: msg}
	}
}
