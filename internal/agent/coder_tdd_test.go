package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFilterTestCommands_RemovesBuildCommands(t *testing.T) {
	cmds := []string{
		"go build ./...",
		"go test ./...",
		"cargo build",
		"cargo test",
	}

	got := filterTestCommands(cmds, "go build ./...")
	want := []string{"go test ./...", "cargo test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterTestCommands() = %#v, want %#v", got, want)
	}
}

func TestValidationSummary_IncludesBuildAndTests(t *testing.T) {
	a := &CoderAgent{}
	summary := a.validationSummary("go build ./...", []string{"go test ./...", "go test -race ./..."})

	checks := []string{
		"Build command:",
		"$ go build ./...",
		"Test commands:",
		"$ go test ./...",
		"$ go test -race ./...",
	}
	for _, check := range checks {
		if !strings.Contains(summary, check) {
			t.Fatalf("validation summary missing %q in %q", check, summary)
		}
	}
}

func TestWriteOneFile_RejectsTestFiles(t *testing.T) {
	root := t.TempDir()
	a := &CoderAgent{root: root}

	err := a.writeOneFile("internal/game/board_test.go", "package game\n")
	if err == nil {
		t.Fatal("expected test file write to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(root, "internal", "game", "board_test.go")); !os.IsNotExist(statErr) {
		t.Fatalf("test file should not be created, stat err = %v", statErr)
	}
}
