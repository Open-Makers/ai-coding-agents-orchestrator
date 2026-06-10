package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNegotiate_SeedContextShowsRequirements(t *testing.T) {
	m := NewNegotiate(nil, "")
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
	m := NewNegotiate(nil, "")
	m.SetSize(80, 24)
	m.SeedContext("   ")
	if len(m.lines) != 0 {
		t.Errorf("expected no lines for blank seed, got %+v", m.lines)
	}
}

func TestNegotiate_CtrlCClearsInput(t *testing.T) {
	m := NewNegotiate(nil, "")
	m.SetSize(80, 24)
	for _, r := range "hello" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.input != "hello" {
		t.Fatalf("setup: expected input %q, got %q", "hello", m.input)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if m.input != "" || m.cursor != 0 {
		t.Errorf("Ctrl+C should clear input: input=%q cursor=%d", m.input, m.cursor)
	}
	// The conversation must stay open (no close message expected).
}

func TestNegotiate_EscWhileWaitingCloses(t *testing.T) {
	m := NewNegotiate(nil, "")
	m.SetSize(80, 24)
	m.SetWaiting()

	// A non-esc key is swallowed while the PM is thinking.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Errorf("expected non-esc key to be swallowed while waiting")
	}

	// Esc must still close the overlay so the user can interrupt and exit.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a command from Esc while waiting")
	}
	if _, ok := cmd().(NegotiateClosedMsg); !ok {
		t.Errorf("Esc while waiting should emit NegotiateClosedMsg")
	}
}

func TestNegotiate_ArrowKeysMoveCursor(t *testing.T) {
	m := NewNegotiate(nil, "")
	m.SetSize(80, 24)
	for _, r := range "abc" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.cursor != 3 {
		t.Fatalf("setup: expected cursor 3, got %d", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.cursor != 1 {
		t.Errorf("two lefts: expected cursor 1, got %d", m.cursor)
	}

	// Insert at cursor position → "aXbc".
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if m.input != "aXbc" {
		t.Errorf("expected insert at cursor to give %q, got %q", "aXbc", m.input)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.cursor != 3 {
		t.Errorf("right: expected cursor 3, got %d", m.cursor)
	}
}

func TestNegotiate_CtrlUClearsToStart(t *testing.T) {
	m := NewNegotiate(nil, "")
	m.SetSize(80, 24)
	for _, r := range "abcd" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // cursor at 3 ("abc|d")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})

	if m.input != "d" || m.cursor != 0 {
		t.Errorf("Ctrl+U should clear to start: input=%q cursor=%d", m.input, m.cursor)
	}
}

func TestNegotiate_SetReadyDoesNotAddBlankLine(t *testing.T) {
	m := NewNegotiate(nil, "")
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

func TestNegotiate_AttachInjectsFileThenClears(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "PROJECT.md"), []byte("existing project description"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sent string
	m := NewNegotiate(func(msg string) { sent = msg }, root)
	m.SetSize(80, 24)

	// Open the picker, confirm the discovered .md file.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	if !m.picking {
		t.Fatal("expected Ctrl+F to open the attach picker")
	}
	m, _ = m.Update(mdPickerDoneMsg{selected: []string{"PROJECT.md"}})
	if m.picking {
		t.Fatal("expected picker to close after confirm")
	}
	if len(m.attached) != 1 {
		t.Fatalf("expected one attachment, got %d", len(m.attached))
	}

	// Sending injects the file content into the PM payload.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(sent, "existing project description") {
		t.Errorf("expected attached file content in payload, got %q", sent)
	}
	if !strings.Contains(sent, "PROJECT.md") {
		t.Errorf("expected attached file name in payload, got %q", sent)
	}

	// Attachments are cleared so they aren't re-sent on the next turn.
	if len(m.attached) != 0 {
		t.Errorf("expected attachments cleared after send, got %d", len(m.attached))
	}
}

func TestNegotiate_SetWaitingShowsThinking(t *testing.T) {
	m := NewNegotiate(nil, "")
	m.SetSize(80, 24)
	m.SeedContext("Resume this project. Plan the remaining work.")
	m.SetWaiting()

	if !strings.Contains(m.View(), "PM is thinking") {
		t.Errorf("expected a thinking indicator while waiting:\n%s", m.View())
	}
	if !strings.Contains(m.vp.View(), "Resume this project") {
		t.Errorf("expected the seeded brief to be visible:\n%s", m.vp.View())
	}
}
