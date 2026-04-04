package agent

import (
	"testing"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

func TestBaseAgent_Emit(t *testing.T) {
	b := bus.New()
	ch := b.Subscribe()

	a := NewBase(bus.RolePlanner, b)
	a.emit(bus.MsgEvent, "test-payload")

	select {
	case msg := <-ch:
		if msg.From != bus.RolePlanner {
			t.Errorf("expected from=planner, got %q", msg.From)
		}
		if msg.Type != bus.MsgEvent {
			t.Errorf("expected type=event, got %q", msg.Type)
		}
		if msg.To != "" {
			t.Errorf("expected broadcast (empty To), got %q", msg.To)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestBaseAgent_EmitToken(t *testing.T) {
	b := bus.New()
	ch := b.Subscribe()

	a := NewBase(bus.RoleCoder, b)
	a.emitToken("hello", false)

	select {
	case msg := <-ch:
		tp, ok := msg.Payload.(bus.TokenPayload)
		if !ok {
			t.Fatalf("expected TokenPayload, got %T", msg.Payload)
		}
		if tp.Text != "hello" || tp.Done {
			t.Errorf("unexpected token: %+v", tp)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
