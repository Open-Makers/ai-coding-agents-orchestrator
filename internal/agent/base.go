package agent

import (
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
)

// BaseAgent holds shared state and helpers for all agents.
type BaseAgent struct {
	role bus.AgentRole
	Bus  *bus.Bus
}

// NewBase creates a BaseAgent for the given role.
func NewBase(role bus.AgentRole, b *bus.Bus) BaseAgent {
	return BaseAgent{role: role, Bus: b}
}

// emit publishes a broadcast message from this agent.
func (a *BaseAgent) emit(typ bus.MessageType, payload any) {
	a.Bus.Publish(bus.NewMessage(a.role, "", typ, payload))
}

// emitToken publishes a streaming token event.
func (a *BaseAgent) emitToken(text string, done bool) {
	a.emit(bus.MsgEvent, bus.TokenPayload{Text: text, Done: done})
}

// emitUsage publishes a token usage event for this agent.
func (a *BaseAgent) emitUsage(usage runner.TokenUsage) {
	a.emit(bus.MsgUsage, bus.AgentUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		Estimated:    usage.Estimated,
	})
}

// Gate publishes a human_gate event (exported so orchestrator can call it if needed).
func (a *BaseAgent) Gate(msg string) {
	a.emit(bus.MsgHumanGate, msg)
}
