package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func setupTreeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"main.go", "internal/app.go", "README.md"} {
		full := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSysmon_TreeFocusAndNavigation(t *testing.T) {
	m := NewSysmon()
	m.SetProjectRoot(setupTreeRoot(t))
	if len(m.projectTree) < 2 {
		t.Fatalf("expected a populated tree, got %d entries", len(m.projectTree))
	}

	if m.TreeFocused() {
		t.Fatal("tree should not start focused")
	}
	m.FocusTree()
	if !m.TreeFocused() {
		t.Fatal("FocusTree should focus the tree")
	}
	// Cursor lands on the first file (not the root dir).
	rel, isDir, ok := m.SelectedTreeEntry()
	if !ok || isDir {
		t.Fatalf("expected first selection to be a file, got rel=%q isDir=%v ok=%v", rel, isDir, ok)
	}

	// Cursor is clamped at the bounds.
	for i := 0; i < 100; i++ {
		m.MoveTreeCursor(1)
	}
	if m.treeCursor != len(m.projectTree)-1 {
		t.Errorf("cursor should clamp to last entry, got %d", m.treeCursor)
	}
	for i := 0; i < 100; i++ {
		m.MoveTreeCursor(-1)
	}
	if m.treeCursor != 0 {
		t.Errorf("cursor should clamp to first entry, got %d", m.treeCursor)
	}

	m.BlurTree()
	if m.TreeFocused() {
		t.Fatal("BlurTree should unfocus the tree")
	}
}

func TestFilePreview_LoadsFileContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewFilePreview(root, 60, 20)
	p.Show("main.go", false)

	view := p.View()
	if !strings.Contains(view, "main.go") {
		t.Errorf("expected file path in preview title:\n%s", view)
	}
	if !strings.Contains(stripANSI(p.vp.View()), "func main()") {
		t.Errorf("expected file content in preview body:\n%s", p.vp.View())
	}
}

func TestFilePreview_DirectoryShowsHint(t *testing.T) {
	p := NewFilePreview(t.TempDir(), 60, 20)
	p.Show("internal", true)
	if !strings.Contains(stripANSI(p.vp.View()), "select a file") {
		t.Errorf("expected directory hint, got:\n%s", p.vp.View())
	}
}
