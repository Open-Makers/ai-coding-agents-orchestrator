package tui

import (
	"strings"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestRenderPhaseBar_ShowsPMApprovalGates(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})
	m.gateArtifact = "vision.md"

	bar := m.renderPhaseBar()
	if !strings.Contains(bar, "VISION") {
		t.Fatalf("expected phase bar to include VISION gate, got %q", bar)
	}
	if !strings.Contains(bar, "MOSCOW") {
		t.Fatalf("expected phase bar to include MOSCOW gate, got %q", bar)
	}
}

func TestView_ShowsApprovalBannerWhenGatePending(t *testing.T) {
	m := New(nil, "", "", nil, config.Config{})
	m.width = 120
	m.height = 30
	m.gateArtifact = "vision.md"

	view := m.View()
	if !strings.Contains(view, "Waiting for approval: vision") {
		t.Fatalf("expected approval banner in main view, got %q", view)
	}
}
