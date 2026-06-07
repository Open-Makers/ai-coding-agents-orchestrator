package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/orchestrator"
)

func TestTaskHistory_RendersTasks(t *testing.T) {
	m := NewTaskHistory([]orchestrator.DoneTask{
		{ID: "t-1", Title: "First task"},
		{ID: "t-2", Title: "Second task"},
	})
	m.SetSize(80, 24)
	view := m.View()
	if !strings.Contains(view, "Done Tasks") {
		t.Errorf("expected title:\n%s", view)
	}
	if !strings.Contains(view, "First task") || !strings.Contains(view, "Second task") {
		t.Errorf("expected task titles:\n%s", view)
	}
}

func TestTaskHistory_EmptyState(t *testing.T) {
	m := NewTaskHistory(nil)
	m.SetSize(80, 24)
	if !strings.Contains(m.View(), "No completed tasks") {
		t.Errorf("expected empty state:\n%s", m.View())
	}
}

func TestTaskHistory_EscCloses(t *testing.T) {
	m := NewTaskHistory([]orchestrator.DoneTask{{ID: "t-1", Title: "x"}})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a close command on Esc")
	}
	if _, ok := cmd().(TaskHistoryClosedMsg); !ok {
		t.Errorf("expected TaskHistoryClosedMsg, got %T", cmd())
	}
}
