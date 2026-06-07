package orchestrator

import (
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/beads"
)

func TestNewRunID(t *testing.T) {
	a := newRunID()
	b := newRunID()
	if a == "" || b == "" {
		t.Fatal("run id should be non-empty")
	}
	if a == b {
		t.Errorf("run ids should be unique, got %q twice", a)
	}
	if len(a) < 8 {
		t.Errorf("run id too short: %q", a)
	}
}

func TestPickInProgress(t *testing.T) {
	inprog := []beads.Issue{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	// First unprocessed is returned.
	got, found := pickInProgress(inprog, map[string]bool{"a": true})
	if !found || got.ID != "b" {
		t.Errorf("expected first unprocessed 'b', got %q found=%v", got.ID, found)
	}

	// None unprocessed → found=false (caller must stop to avoid a loop).
	_, found = pickInProgress(inprog, map[string]bool{"a": true, "b": true, "c": true})
	if found {
		t.Error("expected found=false when all in_progress beads were already processed")
	}

	// Empty input → not found.
	if _, found := pickInProgress(nil, map[string]bool{}); found {
		t.Error("expected found=false for empty input")
	}
}
