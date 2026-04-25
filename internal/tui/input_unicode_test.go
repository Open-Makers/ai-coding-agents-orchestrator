package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNegotiateModel_AllowsDiacritics(t *testing.T) {
	m := NewNegotiate(nil)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ąćńźż")})

	if m.input != "ąćńźż" {
		t.Fatalf("expected diacritics in negotiate input, got %q", m.input)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.input != "ąćńź" {
		t.Fatalf("expected rune-aware backspace, got %q", m.input)
	}
}

func TestChatModel_AllowsDiacritics(t *testing.T) {
	m := NewChat(nil, "")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ążółć")})

	if m.input != "ążółć" {
		t.Fatalf("expected diacritics in chat input, got %q", m.input)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.input != "ążół" {
		t.Fatalf("expected rune-aware backspace, got %q", m.input)
	}
}

func TestNegotiateModel_CtrlAAcceptsImmediately(t *testing.T) {
	var sent string
	m := NewNegotiate(func(msg string) { sent = msg })

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})

	if !m.waiting {
		t.Fatal("expected negotiate model to wait for PM response after Ctrl+A")
	}
	if len(m.lines) != 1 || m.lines[0].role != "user" {
		t.Fatal("expected Ctrl+A to append a synthetic user confirmation line")
	}
	if m.lines[0].content != negotiateAcceptNowMessage {
		t.Fatalf("unexpected synthetic confirmation message: %q", m.lines[0].content)
	}
	if sent != negotiateAcceptNowMessage {
		t.Fatalf("expected Ctrl+A to send synthetic confirmation, got %q", sent)
	}
}

func TestTrimLastRune(t *testing.T) {
	if got := trimLastRune("ąć"); got != "ą" {
		t.Fatalf("expected %q, got %q", "ą", got)
	}
	if got := trimLastRune(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
