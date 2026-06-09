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

	a := NewBase(bus.RoleQA, b)
	a.emit(bus.MsgEvent, "test-payload")

	select {
	case msg := <-ch:
		if msg.From != bus.RoleQA {
			t.Errorf("expected from=qa, got %q", msg.From)
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

	a := NewBase(bus.RoleQA, b)
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

func TestBaseAgent_CollectStream_EmitsIncrementalUsage(t *testing.T) {
	b := bus.New()
	sub := b.Subscribe()

	a := NewBase(bus.RoleCoder, b)
	in := make(chan runner.Token, 8)
	in <- runner.Token{Usage: &runner.TokenUsage{InputTokens: 100, Estimated: true}}
	in <- runner.Token{Text: "partial "}
	in <- runner.Token{Usage: &runner.TokenUsage{OutputTokens: 10, Estimated: true}}
	in <- runner.Token{Text: "answer"}
	in <- runner.Token{Usage: &runner.TokenUsage{OutputTokens: 5, Estimated: true}}
	in <- runner.Token{Done: true, Usage: &runner.TokenUsage{OutputTokens: 2, Estimated: true}}
	close(in)

	out, err := a.collectStream(in)
	if err != nil {
		t.Fatalf("collectStream error: %v", err)
	}
	if out != "partial answer" {
		t.Errorf("collected text: got %q", out)
	}

	// Drain the bus (buffered, non-blocking publish) and tally usage events.
	var totalOut, usageEvents int
	deadline := time.After(time.Second)
drain:
	for {
		select {
		case msg := <-sub:
			if msg.Type == bus.MsgUsage {
				if u, ok := msg.Payload.(bus.AgentUsage); ok {
					totalOut += u.OutputTokens
					usageEvents++
				}
			}
		case <-deadline:
			break drain
		default:
			break drain
		}
	}

	// One input + three output usage events must have been forwarded live,
	// not collapsed into a single final event.
	if usageEvents < 4 {
		t.Errorf("expected >=4 usage events (1 input + 3 output), got %d", usageEvents)
	}
	if totalOut != 17 {
		t.Errorf("summed output usage: got %d, want 17", totalOut)
	}
}
