package multiplexer

import (
	"os/exec"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
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
	defer func() { _ = mx.Close() }()

	roles := []bus.AgentRole{"planner", "coder", "tester", "reviewer"}
	for _, role := range roles {
		pane, err := mx.NewPane(role)
		if err != nil {
			t.Fatalf("NewPane(%s): %v", role, err)
		}

		if err := mx.WriteToPane(pane, "echo hello"); err != nil {
			t.Fatalf("WriteToPane(%s): %v", role, err)
		}
	}
}
