package tui

import (
	"strings"
	"testing"
)

func TestNegotiate_SeedContextShowsRequirements(t *testing.T) {
	m := NewNegotiate(nil)
	m.SetSize(80, 24)
	m.SeedContext("# Task\nBuild a thing")

	if len(m.lines) != 1 || m.lines[0].role != "context" {
		t.Fatalf("expected one context line, got %+v", m.lines)
	}
	if !strings.Contains(m.vp.View(), "Build a thing") {
		t.Errorf("expected requirements content in viewport:\n%s", m.vp.View())
	}
}

func TestNegotiate_SeedContextIgnoresEmpty(t *testing.T) {
	m := NewNegotiate(nil)
	m.SetSize(80, 24)
	m.SeedContext("   ")
	if len(m.lines) != 0 {
		t.Errorf("expected no lines for blank seed, got %+v", m.lines)
	}
}

func TestNegotiate_SetReadyDoesNotAddBlankLine(t *testing.T) {
	m := NewNegotiate(nil)
	m.SetSize(80, 24)
	m.SeedContext("requirements here")
	before := len(m.lines)

	m.SetReady() // PM returned no question

	if len(m.lines) != before {
		t.Errorf("SetReady must not append a line: before=%d after=%d", before, len(m.lines))
	}
	if m.waiting {
		t.Error("SetReady should clear the waiting state so the user can act")
	}
}
