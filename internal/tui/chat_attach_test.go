package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFindMarkdownFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# readme")
	mustWrite(t, filepath.Join(root, "doc", "guide.markdown"), "guide")
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	mustWrite(t, filepath.Join(root, "node_modules", "pkg", "skip.md"), "skip")

	got := findMarkdownFiles(root)
	want := []string{"README.md", filepath.Join("doc", "guide.markdown")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("findMarkdownFiles = %v, want %v", got, want)
	}
}

func TestLoadAttachmentsTruncates(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("a", maxAttachBytes+100)
	mustWrite(t, filepath.Join(root, "big.md"), big)

	got := loadAttachments(root, []string{"big.md", "missing.md"})
	if len(got) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(got))
	}
	if !strings.HasSuffix(got[0].content, "…(truncated)") {
		t.Fatalf("expected truncation marker, got tail %q", got[0].content[len(got[0].content)-20:])
	}
}

func TestAttachmentBlock(t *testing.T) {
	if attachmentBlock(nil) != "" {
		t.Fatal("empty attachments should yield empty block")
	}
	block := attachmentBlock([]chatAttachment{{rel: "a.md", content: "hello"}})
	if !strings.Contains(block, "## a.md") || !strings.Contains(block, "hello") {
		t.Fatalf("block missing content: %q", block)
	}
}

func TestMDPickerToggleAndConfirm(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "a")
	mustWrite(t, filepath.Join(root, "b.md"), "b")

	p := newMDPicker(root, nil, 40, 10)
	// cursor on a.md, toggle select
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeySpace})
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd().(mdPickerDoneMsg)
	if msg.cancel {
		t.Fatal("expected confirm, got cancel")
	}
	if len(msg.selected) != 1 || msg.selected[0] != "a.md" {
		t.Fatalf("expected [a.md], got %v", msg.selected)
	}
}

func TestMDPickerCancel(t *testing.T) {
	p := newMDPicker(t.TempDir(), nil, 40, 10)
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !cmd().(mdPickerDoneMsg).cancel {
		t.Fatal("expected cancel")
	}
}

func TestChatAttachFlow(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "spec.md"), "the spec")

	c := NewChat(nil, "sys").WithFileContext(root)
	c.SetSize(80, 24)

	// Open picker.
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	if !c.picking {
		t.Fatal("Ctrl+F should open picker")
	}
	// Select first file and confirm.
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeySpace})
	var cmd tea.Cmd
	c, cmd = c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	c, _ = c.Update(cmd())
	if c.picking {
		t.Fatal("confirm should close picker")
	}
	if len(c.attached) != 1 || c.attached[0].rel != "spec.md" {
		t.Fatalf("expected spec.md attached, got %v", c.attached)
	}
	if !strings.Contains(c.View(), "spec.md") {
		t.Fatal("view should list attached file")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMDPicker_ManualPathEntryOutsideRoot(t *testing.T) {
	root := t.TempDir()
	// A description file living OUTSIDE the repo root.
	outside := t.TempDir()
	descPath := filepath.Join(outside, "PROJECT.md")
	mustWrite(t, descPath, "external project description")

	p := newMDPicker(root, nil, 80, 24)

	// Enter manual-path mode and type the absolute path.
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !p.entering {
		t.Fatal("expected Ctrl+O to enter manual path mode")
	}
	for _, r := range descPath {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if p.entering {
		t.Fatal("expected entry mode to close after a valid path")
	}
	if !p.selected[descPath] {
		t.Fatalf("expected the manual path to be selected, files=%v", p.files)
	}

	// Confirming yields the path, and loadAttachments reads it from outside root.
	att := loadAttachments(root, []string{descPath})
	if len(att) != 1 || !strings.Contains(att[0].content, "external project description") {
		t.Fatalf("expected to load the outside file, got %+v", att)
	}
}

func TestMDPicker_ManualPathEntryRejectsMissing(t *testing.T) {
	p := newMDPicker(t.TempDir(), nil, 80, 24)
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	for _, r := range "does-not-exist.md" {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !p.entering {
		t.Error("expected to stay in entry mode on a missing file")
	}
	if p.entryErr == "" {
		t.Error("expected an error message for a missing file")
	}
}
