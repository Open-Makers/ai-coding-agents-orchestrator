package agent

import (
	"strings"

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

func (a *BaseAgent) emitOutput(text string) {
	if text != "" {
		a.emitToken(text, false)
	}
	a.emitToken("", true)
}

// Gate publishes a human_gate event (exported so orchestrator can call it if needed).
func (a *BaseAgent) Gate(msg string) {
	a.emit(bus.MsgHumanGate, msg)
}

// collectStream reads all tokens from a completion stream, publishing each
// token via the bus and returning the full concatenated text.
func (a *BaseAgent) collectStream(ch <-chan runner.Token) (string, error) {
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return sb.String(), tok.Error
		}
		if tok.Done {
			if tok.Usage != nil {
				a.emitUsage(*tok.Usage)
			}
			break
		}
		a.emitToken(tok.Text, false)
		sb.WriteString(tok.Text)
	}
	a.emitToken("", true)
	return sb.String(), nil
}
