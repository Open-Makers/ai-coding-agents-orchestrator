package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestAgentPanel_WrapsLongLinesToViewportWidth(t *testing.T) {
	m := NewAgentPanel("pm")
	m.SetSize(42, 10)
	m.addLine("To create a simple, engaging, and focused single-player competitive experience based on the classic game of Tic-Tac-Toe.")
	m.syncViewport()

	for _, line := range strings.Split(m.vp.View(), "\n") {
		if ansi.StringWidth(line) > m.vp.Width {
			t.Fatalf("line exceeds viewport width %d: %q", m.vp.Width, line)
		}
	}
}
