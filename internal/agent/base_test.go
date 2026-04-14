package agent

import (
	"testing"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/runner"
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

func TestBaseAgent_EmitUsage(t *testing.T) {
	b := bus.New()
	ch := b.Subscribe()

	a := NewBase(bus.RolePlanner, b)
	a.emitUsage(runner.TokenUsage{InputTokens: 200, OutputTokens: 80, Estimated: false})

	select {
	case msg := <-ch:
		if msg.Type != bus.MsgUsage {
			t.Fatalf("expected MsgUsage, got %q", msg.Type)
		}
		u, ok := msg.Payload.(bus.AgentUsage)
		if !ok {
			t.Fatalf("expected AgentUsage payload, got %T", msg.Payload)
		}
		if u.InputTokens != 200 || u.OutputTokens != 80 {
			t.Errorf("unexpected usage: %+v", u)
		}
		if u.Estimated {
			t.Error("Estimated should be false")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestBaseAgent_CollectStream_EmitsUsage(t *testing.T) {
	b := bus.New()
	busCh := b.Subscribe()

	a := NewBase(bus.RoleCoder, b)

	tokenCh := make(chan runner.Token, 3)
	tokenCh <- runner.Token{Text: "hello "}
	tokenCh <- runner.Token{Text: "world"}
	tokenCh <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 50, OutputTokens: 10}}
	close(tokenCh)

	text, err := a.collectStream(tokenCh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text: want %q, got %q", "hello world", text)
	}

	var foundUsage bool
	deadline := time.After(time.Second)
	for !foundUsage {
		select {
		case msg := <-busCh:
			if msg.Type == bus.MsgUsage {
				u := msg.Payload.(bus.AgentUsage)
				if u.InputTokens == 50 && u.OutputTokens == 10 {
					foundUsage = true
				}
			}
		case <-deadline:
			t.Fatal("timeout waiting for MsgUsage")
		}
	}
}
