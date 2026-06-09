package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Toggling away from a mode and back must preserve the value the user typed,
// not reset it to the default. Reproduces the reported bug where editing the
// context limit, switching to RAM, and switching back lost the context value.
func TestModelMemory_TogglePreservesPerModeValue(t *testing.T) {
	m := newModelMemoryModel(config.Config{})
	// Start in context mode.
	for m.mode != mmModeContext {
		m, _ = m.Update(key("l"))
	}

	// Clear and type a custom context value.
	m.value.SetValue("")
	for _, r := range "12345" {
		m, _ = m.Update(key(string(r)))
	}
	if got := m.value.Value(); got != "12345" {
		t.Fatalf("context input: want 12345, got %q", got)
	}

	// Toggle to RAM and back to context.
	m, _ = m.Update(key("l")) // context -> off (or ram, order-dependent)
	m, _ = m.Update(key("h")) // back

	// Find context mode again and assert the value survived.
	startMode := m.mode
	for m.mode != mmModeContext {
		m, _ = m.Update(key("l"))
		if m.mode == startMode {
			t.Fatal("could not return to context mode")
		}
	}
	if got := m.value.Value(); got != "12345" {
		t.Errorf("context value not preserved across toggle: want 12345, got %q", got)
	}
}

func TestModelMemory_ResultUsesActiveMode(t *testing.T) {
	m := newModelMemoryModel(config.Config{})
	for m.mode != mmModeContext {
		m, _ = m.Update(key("l"))
	}
	m.value.SetValue("")
	for _, r := range "16384" {
		m, _ = m.Update(key(string(r)))
	}
	res := m.result()
	if res.Mode != "context" || res.MaxContextTokens != 16384 {
		t.Errorf("result: want context/16384, got %s/%d", res.Mode, res.MaxContextTokens)
	}
}
