package agent

import (
	"strings"
	"testing"
)

func TestNormalizePMOutput_ConvertsJSONToSections(t *testing.T) {
	input := "```json\n{\n  \"VISION\": {\n    \"Problem statement\": \"Build TicTacToe\"\n  },\n  \"MOSCOW\": {\n    \"#1 Must Have\": {\n      \"Description\": \"Playable game\"\n    }\n  }\n}\n```"

	got := normalizePMOutput(input)
	if !strings.Contains(got, "===VISION===") {
		t.Fatalf("expected VISION section, got %q", got)
	}
	if !strings.Contains(got, "===MOSCOW===") {
		t.Fatalf("expected MOSCOW section, got %q", got)
	}
	if !strings.Contains(got, "Problem statement: Build TicTacToe") {
		t.Fatalf("expected problem statement in markdown, got %q", got)
	}
}

func TestNormalizePMOutput_LeavesMarkdownUnchanged(t *testing.T) {
	input := "===VISION===\n- Problem statement: Build TicTacToe\n\n===MOSCOW===\n## Must Have"
	got := normalizePMOutput(input)
	if got != input {
		t.Fatalf("expected markdown output unchanged, got %q", got)
	}
}

func TestFormatPMDisplay_StripsTechnicalDelimiters(t *testing.T) {
	input := "===VISION===\n- Problem statement: Build TicTacToe\n\n===MOSCOW===\n## Must Have\n1. Playable game"

	got := formatPMDisplay(input)
	if strings.Contains(got, "===VISION===") || strings.Contains(got, "===MOSCOW===") {
		t.Fatalf("expected delimiters removed, got %q", got)
	}
	if !strings.Contains(got, "VISION") || !strings.Contains(got, "MOSCOW") {
		t.Fatalf("expected readable section titles, got %q", got)
	}
}
