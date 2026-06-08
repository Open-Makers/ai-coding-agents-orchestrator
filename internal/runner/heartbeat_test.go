package runner

import (
	"strings"
	"testing"
	"time"
)

func TestWithHeartbeat_EmitsDuringSilence(t *testing.T) {
	in := make(chan Token)
	out := withHeartbeat(in, 20*time.Millisecond)

	// Stay silent long enough for at least one heartbeat, then finish.
	go func() {
		time.Sleep(70 * time.Millisecond)
		in <- Token{Done: true}
		close(in)
	}()

	var beats int
	var sawDone bool
	for tok := range out {
		if tok.Done {
			sawDone = true
		}
		if strings.Contains(tok.Reasoning, "still working") {
			beats++
		}
	}
	if beats == 0 {
		t.Error("expected at least one heartbeat during silence")
	}
	if !sawDone {
		t.Error("expected the Done token to pass through")
	}
}

func TestWithHeartbeat_PassesTokensAndClosesOnDone(t *testing.T) {
	in := make(chan Token, 3)
	in <- Token{Text: "hello"}
	in <- Token{Done: true}
	close(in)

	out := withHeartbeat(in, time.Second)
	var text string
	for tok := range out {
		text += tok.Text
	}
	if text != "hello" {
		t.Errorf("expected real token forwarded, got %q", text)
	}
}

func TestToolUseNotices(t *testing.T) {
	content := []claudeStreamContent{
		{Type: "text", Text: "thinking"},
		{Type: "tool_use", Name: "Read", Input: []byte(`{"file_path":"internal/ai/ai.go"}`)},
		{Type: "tool_use", Name: "Bash", Input: []byte(`{"command":"go build ./..."}`)},
	}
	notices := toolUseNotices(content)
	if len(notices) != 2 {
		t.Fatalf("expected 2 tool notices, got %d: %v", len(notices), notices)
	}
	if !strings.Contains(notices[0], "Read") || !strings.Contains(notices[0], "ai.go") {
		t.Errorf("unexpected notice: %q", notices[0])
	}
	if !strings.Contains(notices[1], "Bash") || !strings.Contains(notices[1], "go build") {
		t.Errorf("unexpected notice: %q", notices[1])
	}
}

func TestShortElapsed(t *testing.T) {
	if got := shortElapsed(12 * time.Second); got != "12s" {
		t.Errorf("want 12s, got %s", got)
	}
	if got := shortElapsed(125 * time.Second); got != "2m05s" {
		t.Errorf("want 2m05s, got %s", got)
	}
}
