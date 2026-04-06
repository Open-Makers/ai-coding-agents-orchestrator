package tui

import "testing"

func TestParseReviseDirective_ValidBlock(t *testing.T) {
	response := `I understand your concern. Let me revise the architecture to use a simpler approach.

===REVISE: architecture.md===
Simplify the database layer to use SQLite instead of PostgreSQL.
Remove the separate caching service.
===END===`

	artifact, feedback := parseReviseDirective(response)
	if artifact != "architecture.md" {
		t.Errorf("expected artifact 'architecture.md', got %q", artifact)
	}
	if feedback == "" {
		t.Error("expected non-empty feedback")
	}
	if !contains(feedback, "SQLite") {
		t.Errorf("feedback should mention SQLite, got %q", feedback)
	}
}

func TestParseReviseDirective_NoDirective(t *testing.T) {
	response := "Sure, the architecture uses a layered approach with clean separation of concerns."

	artifact, feedback := parseReviseDirective(response)
	if artifact != "" {
		t.Errorf("expected empty artifact, got %q", artifact)
	}
	if feedback != "" {
		t.Errorf("expected empty feedback, got %q", feedback)
	}
}

func TestParseReviseDirective_InvalidArtifact(t *testing.T) {
	response := `===REVISE: nonexistent.md===
Some feedback
===END===`

	artifact, _ := parseReviseDirective(response)
	if artifact != "" {
		t.Errorf("expected empty artifact for invalid name, got %q", artifact)
	}
}

func TestParseReviseDirective_AllValidArtifacts(t *testing.T) {
	validArtifacts := []string{
		"vision.md",
		"moscow.md",
		"architecture.md",
		"implementation_plan.md",
		"prompts.md",
	}
	for _, name := range validArtifacts {
		response := "===REVISE: " + name + "===\nfix it\n===END==="
		artifact, feedback := parseReviseDirective(response)
		if artifact != name {
			t.Errorf("expected %q, got %q", name, artifact)
		}
		if feedback != "fix it" {
			t.Errorf("expected 'fix it', got %q", feedback)
		}
	}
}

func TestParseReviseDirective_WithoutEndMarker(t *testing.T) {
	response := `===REVISE: prompts.md===
Add more detail to stage 3 about error handling.`

	artifact, feedback := parseReviseDirective(response)
	if artifact != "prompts.md" {
		t.Errorf("expected 'prompts.md', got %q", artifact)
	}
	if feedback == "" {
		t.Error("expected non-empty feedback even without ===END===")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
