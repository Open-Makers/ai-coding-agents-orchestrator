package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withColor forces a colour profile so lipgloss emits ANSI in the test env
// (no TTY otherwise). Returns a restore func.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestStyleMarkdownLine_Headings(t *testing.T) {
	withColor(t)
	cases := map[string]string{
		"# Task Plan":            "# Task Plan",
		"## T1 — Define Structs": "## T1 — Define Structs",
		"### Subsection":         "### Subsection",
	}
	for in, wantText := range cases {
		got := styleMarkdownLine(in)
		if got == in {
			t.Errorf("expected %q to be styled (ANSI added), got identical", in)
		}
		if stripANSI(got) != wantText {
			t.Errorf("styling changed visible text: got %q want %q", stripANSI(got), wantText)
		}
	}
}

func TestStyleMarkdownInline_StripsBoldMarkers(t *testing.T) {
	got := styleMarkdownInline("decomposed into **8** sub-task(s)")
	if strings.Contains(stripANSI(got), "**") {
		t.Errorf("expected ** markers stripped, got %q", stripANSI(got))
	}
	if !strings.Contains(stripANSI(got), "8") {
		t.Errorf("expected bold content preserved, got %q", stripANSI(got))
	}
}

func TestStyleMarkdownLine_LabelRow(t *testing.T) {
	withColor(t)
	got := styleMarkdownLine("Priority: 1")
	if got == "Priority: 1" {
		t.Error("expected label row to be styled")
	}
	if stripANSI(got) != "Priority: 1" {
		t.Errorf("label styling changed text: %q", stripANSI(got))
	}

	got = styleMarkdownLine("Depends on: T1")
	if stripANSI(got) != "Depends on: T1" {
		t.Errorf("label styling changed text: %q", stripANSI(got))
	}
}

func TestStyleMarkdownLine_BulletPreservesIndent(t *testing.T) {
	got := styleMarkdownLine("  - first item")
	if !strings.HasPrefix(got, "  ") {
		t.Errorf("expected leading indent preserved, got %q", got)
	}
	if stripANSI(got) != "  - first item" {
		t.Errorf("bullet styling changed text: %q", stripANSI(got))
	}
}

func TestStyleArtifactContent_JSONHighlighted(t *testing.T) {
	out := styleArtifactContent("sub_tasks.json", "{\n  \"key\": \"T1\"\n}")
	if out == "{\n  \"key\": \"T1\"\n}" {
		t.Error("expected JSON content to be syntax-highlighted")
	}
	if !strings.Contains(stripANSI(out), "\"key\"") {
		t.Errorf("expected JSON text preserved, got %q", stripANSI(out))
	}
}

func TestStyleArtifactContent_PlainLineUnchangedText(t *testing.T) {
	out := styleArtifactContent("task_plan.md", "just a sentence")
	if stripANSI(out) != "just a sentence" {
		t.Errorf("plain text should be preserved, got %q", stripANSI(out))
	}
}
