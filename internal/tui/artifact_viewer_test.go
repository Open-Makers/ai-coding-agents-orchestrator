package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWrapLine_ShortLine(t *testing.T) {
	lines := wrapLine("short line", 80)
	if len(lines) != 1 || lines[0] != "short line" {
		t.Errorf("expected single unchanged line, got %v", lines)
	}
}

func TestWrapLine_EmptyLine(t *testing.T) {
	lines := wrapLine("", 80)
	if len(lines) != 1 || lines[0] != "" {
		t.Errorf("expected single empty line, got %v", lines)
	}
}

func TestWrapLine_LongLine(t *testing.T) {
	line := "This is a very long line that should be wrapped because it exceeds the maximum width"
	lines := wrapLine(line, 40)
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %d", len(lines))
	}
	for i, l := range lines {
		w := len(l) // ASCII-only, so len == visible width
		if w > 40 {
			t.Errorf("line %d exceeds maxW: %q (width=%d)", i, l, w)
		}
	}
}

func TestWrapLine_PreservesIndentation(t *testing.T) {
	line := "    indented text that is long enough to require wrapping at a reasonable width"
	lines := wrapLine(line, 40)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "    ") {
		t.Errorf("first line should preserve indent: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("continuation should preserve indent: %q", lines[1])
	}
}

func TestWrapContent_MultipleLines(t *testing.T) {
	content := "short\nthis is a much longer line that needs to wrap at forty chars wide\nend"
	wrapped := wrapContent(content, 40)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 4 { // short + 2+ wrapped + end
		t.Errorf("expected at least 4 lines after wrapping, got %d", len(lines))
	}
	if lines[0] != "short" {
		t.Errorf("first line should be unchanged: %q", lines[0])
	}
	if lines[len(lines)-1] != "end" {
		t.Errorf("last line should be unchanged: %q", lines[len(lines)-1])
	}
}

func TestWrapContent_ZeroWidth(t *testing.T) {
	content := "should not crash"
	result := wrapContent(content, 0)
	if result != content {
		t.Errorf("zero width should return original, got %q", result)
	}
}

func TestTruncateToWidth_Short(t *testing.T) {
	result := truncateToWidth("hello", 10)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncateToWidth_Long(t *testing.T) {
	result := truncateToWidth("this is a long string", 10)
	if len(result) > 10+3 { // allow for multi-byte "…"
		t.Errorf("expected truncated result, got %q", result)
	}
	if !strings.HasSuffix(result, "…") {
		t.Errorf("truncated result should end with '…': %q", result)
	}
}

func TestTruncateToWidth_ZeroWidth(t *testing.T) {
	result := truncateToWidth("test", 0)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractMDFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Please review prompts.md", "prompts.md"},
		{"Check implementation_plan.md now", "implementation_plan.md"},
		{"no markdown here", ""},
		{"multiple files.md and other.md", "files.md"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractMDFilename(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestNewArtifactViewer_WrapsContent(t *testing.T) {
	wsDir := t.TempDir()
	longLine := strings.Repeat("word ", 30) // ~150 chars
	content := "# Title\n\n" + longLine + "\n\nEnd."
	_ = os.WriteFile(filepath.Join(wsDir, "test.md"), []byte(content), 0o644)

	m := newArtifactViewer(wsDir, "test.md", 80, 24, nil)

	vpContent := m.vp.View()
	for _, line := range strings.Split(vpContent, "\n") {
		if len(line) > 80 {
			t.Errorf("viewport line exceeds width: %q (len=%d)", line, len(line))
		}
	}
	if m.rawContent != content {
		t.Error("rawContent should store original unwrapped content")
	}
}

func TestNewArtifactViewer_MissingFile(t *testing.T) {
	wsDir := t.TempDir()
	m := newArtifactViewer(wsDir, "missing.md", 80, 24, nil)

	if !containsText(m.vp.View(), "not generated") {
		t.Error("missing file should show error message")
	}
}

func TestNewArtifactViewer_HintFallback(t *testing.T) {
	wsDir := t.TempDir()
	m := newArtifactViewer(wsDir, "no markdown extension here", 80, 24, nil)

	if m.filename != "review" {
		t.Errorf("expected filename 'review', got %q", m.filename)
	}
}

func TestArtifactViewer_ReloadContent(t *testing.T) {
	wsDir := t.TempDir()
	filePath := filepath.Join(wsDir, "test.md")
	_ = os.WriteFile(filePath, []byte("original"), 0o644)

	m := newArtifactViewer(wsDir, "test.md", 80, 24, nil)
	if !containsText(m.vp.View(), "original") {
		t.Error("should show original content")
	}

	_ = os.WriteFile(filePath, []byte("updated content"), 0o644)
	m.reloadContent()

	if !containsText(m.vp.View(), "updated") {
		t.Error("should show updated content after reload")
	}
	if m.rawContent != "updated content" {
		t.Errorf("rawContent should be updated, got %q", m.rawContent)
	}
}

func TestArtifactViewer_RewrapOnResize(t *testing.T) {
	wsDir := t.TempDir()
	longLine := strings.Repeat("word ", 20)
	_ = os.WriteFile(filepath.Join(wsDir, "test.md"), []byte(longLine), 0o644)

	m := newArtifactViewer(wsDir, "test.md", 120, 24, nil)
	wideWrapped := wrapContent(m.rawContent, 120-6)

	// Resize to narrow.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	narrowWrapped := wrapContent(m.rawContent, 40-6)

	wideLines := strings.Count(wideWrapped, "\n")
	narrowLines := strings.Count(narrowWrapped, "\n")
	if narrowLines <= wideLines {
		t.Errorf("narrow wrap should produce more lines: wide=%d narrow=%d", wideLines, narrowLines)
	}
}

func TestArtifactViewer_Navigation(t *testing.T) {
	wsDir := t.TempDir()
	// Create content tall enough to scroll.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, strings.Repeat("x", 20))
	}
	_ = os.WriteFile(filepath.Join(wsDir, "big.md"), []byte(strings.Join(lines, "\n")), 0o644)

	m := newArtifactViewer(wsDir, "big.md", 80, 10, nil)

	// Scroll down.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})

	// Go to bottom.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	pct := m.vp.ScrollPercent()
	if pct < 0.9 {
		t.Errorf("expected near bottom after G, got %.2f", pct)
	}

	// Go to top.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	pct = m.vp.ScrollPercent()
	if pct > 0.1 {
		t.Errorf("expected near top after g, got %.2f", pct)
	}
}

func TestArtifactViewer_ApproveClose(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "r.md"), []byte("ok"), 0o644)

	m := newArtifactViewer(wsDir, "r.md", 80, 24, nil)

	// Ctrl+A should produce approve message.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd == nil {
		t.Fatal("expected command from Ctrl+A")
	}
	msg := cmd()
	if closed, ok := msg.(artifactViewerClosedMsg); !ok || !closed.approved {
		t.Error("Ctrl+A should produce approved=true message")
	}
}

func TestArtifactViewer_EscClose(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "r.md"), []byte("ok"), 0o644)

	m := newArtifactViewer(wsDir, "r.md", 80, 24, nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected command from Esc")
	}
	msg := cmd()
	if closed, ok := msg.(artifactViewerClosedMsg); !ok || closed.approved {
		t.Error("Esc should produce approved=false message")
	}
}

func TestArtifactViewer_ChatMode(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "r.md"), []byte("content"), 0o644)

	revise := func(artifact, feedback string) error { return nil }
	m := newArtifactViewer(wsDir, "r.md", 80, 24, revise)

	// Enter chat mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !m.chatMode {
		t.Error("expected chat mode after 'c'")
	}

	// Esc exits chat mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.chatMode {
		t.Error("expected chat mode to end after Esc")
	}
}

func TestArtifactViewer_ChatModeDisabledWithoutRevise(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "r.md"), []byte("content"), 0o644)

	m := newArtifactViewer(wsDir, "r.md", 80, 24, nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	if m.chatMode {
		t.Error("chat mode should not activate without revise function")
	}
}

func TestArtifactViewer_EditMode(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "r.md"), []byte("content"), 0o644)

	m := newArtifactViewer(wsDir, "r.md", 80, 24, nil)

	// Enter edit mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if !m.editMode {
		t.Error("expected edit mode after 'e'")
	}

	// Esc exits edit mode.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.editMode {
		t.Error("expected edit mode to end after Esc")
	}
}

func TestArtifactViewer_View(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "test.md"), []byte("# Hello\nWorld"), 0o644)

	m := newArtifactViewer(wsDir, "test.md", 80, 24, nil)
	view := m.View()

	if !containsText(view, "test.md") {
		t.Error("view should contain filename")
	}
	if !containsText(view, "Hello") {
		t.Error("view should contain file content")
	}
}

func TestArtifactViewer_VpHeight(t *testing.T) {
	m := ArtifactViewerModel{height: 24}

	normalH := m.vpHeight(false)
	chatH := m.vpHeight(true)

	if chatH >= normalH {
		t.Errorf("chat mode should reduce viewport height: normal=%d chat=%d", normalH, chatH)
	}

	m.height = 3
	if h := m.vpHeight(false); h < 2 {
		t.Errorf("vpHeight should be at least 2, got %d", h)
	}
}

func TestDiscoverArtifacts_PipelineOrder(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "prompts.md"), []byte("p"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "architecture.md"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("r"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "implementation_plan.md"), []byte("i"), 0o644)

	artifacts, idx := discoverArtifacts(wsDir, "prompts.md")

	expected := []string{"requirements.md", "architecture.md", "implementation_plan.md", "prompts.md"}
	if len(artifacts) != len(expected) {
		t.Fatalf("expected %d artifacts, got %d: %v", len(expected), len(artifacts), artifacts)
	}
	for i, name := range expected {
		if artifacts[i] != name {
			t.Errorf("artifacts[%d]: expected %q, got %q", i, name, artifacts[i])
		}
	}
	if idx != 3 {
		t.Errorf("expected idx=3 for prompts.md, got %d", idx)
	}
}

func TestDiscoverArtifacts_IncludesExtraFiles(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("r"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "custom_notes.md"), []byte("n"), 0o644)

	artifacts, _ := discoverArtifacts(wsDir, "requirements.md")

	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d: %v", len(artifacts), artifacts)
	}
	if artifacts[0] != "requirements.md" {
		t.Errorf("first should be requirements.md, got %q", artifacts[0])
	}
	if artifacts[1] != "custom_notes.md" {
		t.Errorf("extra file should be appended, got %q", artifacts[1])
	}
}

func TestDiscoverArtifacts_CurrentNotOnDisk(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("r"), 0o644)

	artifacts, idx := discoverArtifacts(wsDir, "missing.md")

	found := false
	for _, a := range artifacts {
		if a == "missing.md" {
			found = true
		}
	}
	if !found {
		t.Error("current file should be added even if not on disk")
	}
	if artifacts[idx] != "missing.md" {
		t.Errorf("idx should point to missing.md, got %q", artifacts[idx])
	}
}

func TestArtifactViewer_NavigatePrevNext(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("req content"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "architecture.md"), []byte("arch content"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "prompts.md"), []byte("prompts content"), 0o644)

	m := newArtifactViewer(wsDir, "prompts.md", 80, 24, nil)

	if m.filename != "prompts.md" {
		t.Fatalf("expected initial file prompts.md, got %q", m.filename)
	}
	if m.gateFilename != "prompts.md" {
		t.Fatalf("gate should be prompts.md, got %q", m.gateFilename)
	}

	// Navigate to previous document.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.filename != "architecture.md" {
		t.Errorf("expected architecture.md after [, got %q", m.filename)
	}
	if !containsText(m.vp.View(), "arch content") {
		t.Error("viewport should show architecture content")
	}

	// Navigate to previous again.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.filename != "requirements.md" {
		t.Errorf("expected requirements.md after [, got %q", m.filename)
	}

	// Can't go before first.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.filename != "requirements.md" {
		t.Errorf("should stay at requirements.md, got %q", m.filename)
	}

	// Navigate forward.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.filename != "architecture.md" {
		t.Errorf("expected architecture.md after ], got %q", m.filename)
	}

	// Navigate to last.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.filename != "prompts.md" {
		t.Errorf("expected prompts.md after ], got %q", m.filename)
	}

	// Can't go past last.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.filename != "prompts.md" {
		t.Errorf("should stay at prompts.md, got %q", m.filename)
	}
}

func TestArtifactViewer_NavigateWithArrowKeys(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("req"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "architecture.md"), []byte("arch"), 0o644)

	m := newArtifactViewer(wsDir, "architecture.md", 80, 24, nil)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.filename != "requirements.md" {
		t.Errorf("left arrow should navigate to previous, got %q", m.filename)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.filename != "architecture.md" {
		t.Errorf("right arrow should navigate to next, got %q", m.filename)
	}
}

func TestArtifactViewer_ApproveReturnsToGate(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("req"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "prompts.md"), []byte("prompts"), 0o644)

	m := newArtifactViewer(wsDir, "prompts.md", 80, 24, nil)

	// Navigate away from gate.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.filename == "prompts.md" {
		t.Fatal("should have navigated away")
	}

	// Approve — should switch back to gate file.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.filename != "prompts.md" {
		t.Errorf("approve should return to gate file, got %q", m.filename)
	}
	if cmd == nil {
		t.Fatal("expected command from Ctrl+A")
	}
}

func TestArtifactViewer_ViewShowsNavigation(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "requirements.md"), []byte("req"), 0o644)
	_ = os.WriteFile(filepath.Join(wsDir, "architecture.md"), []byte("arch"), 0o644)

	m := newArtifactViewer(wsDir, "architecture.md", 80, 24, nil)
	view := m.View()

	if !containsText(view, "[2/2]") {
		t.Error("view should show navigation position [2/2]")
	}
	if !containsText(view, "[/]") {
		t.Error("hints should contain [/] prev/next")
	}
}

func TestArtifactViewer_SingleFileNoNavHints(t *testing.T) {
	wsDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(wsDir, "only.md"), []byte("only"), 0o644)

	m := newArtifactViewer(wsDir, "only.md", 80, 24, nil)
	view := m.View()

	if containsText(view, "[/]") {
		t.Error("single file should not show navigation hints")
	}
	if containsText(view, "[1/1]") {
		t.Error("single file should not show position indicator")
	}
}
