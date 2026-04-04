package agent

import (
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
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

// gate publishes a human_gate event and blocks until approved via the returned channel.
// The caller must send on the returned channel to unblock.
func (a *BaseAgent) gate(msg string) {
	a.emit(bus.MsgHumanGate, msg)
}
