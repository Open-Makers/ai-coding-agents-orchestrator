package multiplexer

import (
	"os/exec"
	"testing"
)

func TestTmuxMultiplexer_SkipIfNoTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH, skipping tmux tests")
	}

	sessionID := "orch-test-tmux-multiplexer"
	mx := New(sessionID)

	if err := mx.CreateSession(sessionID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer mx.Close()

	pane, err := mx.NewPane("planner")
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}

	if err := mx.WriteToPane(pane, "echo hello"); err != nil {
		t.Fatalf("WriteToPane: %v", err)
	}
}
