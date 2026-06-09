package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderMarkdownChatLine_StylesStructure(t *testing.T) {
	base := lipgloss.NewStyle().Foreground(crt.primary)
	content := strings.Join([]string{
		"## Must Have",
		"- First **bold** item",
		"1. Numbered step",
		"Priority: high",
		"plain sentence",
	}, "\n")

	out := renderMarkdownChatLine("pm › ", content, lipgloss.NewStyle(), base, 80)

	// Visible text (ANSI stripped) must preserve all content.
	visible := stripANSIForTest(out)
	for _, want := range []string{"Must Have", "First bold item", "Numbered step", "Priority:", "plain sentence"} {
		if !strings.Contains(visible, want) {
			t.Errorf("rendered output missing %q\n%s", want, visible)
		}
	}
	// Bullet marker is normalised to "•".
	if !strings.Contains(visible, "• First") {
		t.Errorf("expected normalised bullet marker, got:\n%s", visible)
	}
	// The "pm › " prefix appears once, on the first line.
	if strings.Count(out, "pm › ") != 1 {
		t.Errorf("prefix should appear exactly once, got %d", strings.Count(out, "pm › "))
	}
}

func TestClassifyMarkdownLine(t *testing.T) {
	base := lipgloss.NewStyle()
	cases := []struct {
		in         string
		wantMarker string // visible marker text ("" if none)
		wantBody   string
	}{
		{"## Should Have", "", "Should Have"},
		{"=== VISION ===", "", "VISION"},
		{"- a bullet", "• ", "a bullet"},
		{"2) second", "2)", "second"},
		{"Priority: high", "Priority:", "high"},
		{"just prose", "", "just prose"},
	}
	for _, c := range cases {
		_, marker, body := classifyMarkdownLine(c.in, base)
		if got := strings.TrimSpace(stripANSIForTest(marker)); got != strings.TrimSpace(c.wantMarker) {
			t.Errorf("classify(%q) marker = %q, want %q", c.in, got, c.wantMarker)
		}
		if body != c.wantBody {
			t.Errorf("classify(%q) body = %q, want %q", c.in, body, c.wantBody)
		}
	}
}

func TestApplyInlineMarkdown_PreservesText(t *testing.T) {
	base := lipgloss.NewStyle()
	got := stripANSIForTest(applyInlineMarkdown("a **b** c `d` e", base))
	if got != "a b c d e" {
		t.Errorf("inline markdown visible text = %q, want %q", got, "a b c d e")
	}
}

// stripANSIForTest removes ANSI escape sequences for assertions on visible text.
func stripANSIForTest(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
