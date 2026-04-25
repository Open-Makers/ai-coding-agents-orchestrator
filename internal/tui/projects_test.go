package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterRecentProjects_RemovesMissingPaths(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")

	projects := []RecentProject{
		{Path: existing, Name: "existing"},
		{Path: missing, Name: "missing"},
	}

	filtered := filterRecentProjects(projects)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 project after filtering, got %d", len(filtered))
	}
	if filtered[0].Path != existing {
		t.Fatalf("expected existing path to remain, got %q", filtered[0].Path)
	}
}

func TestFilterRecentProjects_RemovesHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	existing := t.TempDir()
	filtered := filterRecentProjects([]RecentProject{
		{Path: home, Name: "home"},
		{Path: existing, Name: "existing"},
	})

	if len(filtered) != 1 {
		t.Fatalf("expected 1 project after filtering, got %d", len(filtered))
	}
	if filtered[0].Path != existing {
		t.Fatalf("expected non-home path to remain, got %q", filtered[0].Path)
	}
}
