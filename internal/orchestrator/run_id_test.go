package orchestrator

import "testing"

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
