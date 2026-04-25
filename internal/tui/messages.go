package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

// BusMessageMsg wraps a bus.Message as a tea.Msg.
type BusMessageMsg struct {
	Msg bus.Message
}

// statusBarTickMsg triggers scroll animation for the status bar marquee.
type statusBarTickMsg struct{}

// statusBarTick returns a tea.Cmd that fires a statusBarTickMsg after a short delay.
func statusBarTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return statusBarTickMsg{}
	})
}

// AgentState represents the display state of an agent panel.
type AgentState string

const (
	AgentWaiting AgentState = "waiting"
	AgentRunning AgentState = "running"
	AgentFixing  AgentState = "fixing"
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
type PickerCancelledMsg struct{}
type PickerSelectedMsg struct {
	Path  string
	IsNew bool
}
type FileSelectedMsg struct{ Path string }

// Git panel messages.
type FileRejectedMsg struct{ Path string }
type GitPanelClosedMsg struct{}
type GitRefreshMsg struct{}

// pipelineReadyMsg is sent after buildAgents completes so that log output
// from agent construction appears inside the TUI, not between TUI sessions.
type pipelineReadyMsg struct {
	p      *orchestrator.Pipeline
	cancel context.CancelFunc
}

// Chat messages.
type ChatTokenMsg struct {
	Text string
	Done bool
}
type ChatClosedMsg struct{}

// PipelineDoneMsg is sent when the pipeline finishes (successfully or with an error).
type PipelineDoneMsg struct {
	Err error
}

// TaskRunnerReadyMsg is sent when the TaskRunner is ready to receive interaction.
type TaskRunnerReadyMsg struct {
	Runner *orchestrator.TaskRunner
	Cancel context.CancelFunc
}

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
