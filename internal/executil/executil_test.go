package executil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitize_Valid(t *testing.T) {
	valid := []string{
		"go test ./...",
		"npm run test",
		"python -m pytest tests/",
		"cargo test",
		"make test",
		"./scripts/test.sh",
		"go test -race -timeout 120s ./...",
		"go mod tidy",
	}
	for _, cmd := range valid {
		if err := Sanitize(cmd); err != nil {
			t.Errorf("expected %q to be valid, got: %v", cmd, err)
		}
	}
}

func TestSanitize_Rejected(t *testing.T) {
	cases := []struct {
		cmd    string
		reason string
	}{
		{"", "empty command"},
		{"   ", "whitespace only"},
		{"go test ./...\nrm -rf /", "embedded newline"},
		{"go test ./...\rrm -rf /", "embedded carriage return"},
		{"go test " + strings.Repeat("x", maxCmdLen), "too long"},
		{"echo \x00hello", "null byte"},
		{"echo \x01hello", "SOH control char"},
	}
	for _, tc := range cases {
		if err := Sanitize(tc.cmd); err == nil {
			t.Errorf("expected %q to be rejected (%s)", tc.cmd, tc.reason)
		}
	}
}

func TestRunner_Run(t *testing.T) {
	dir := t.TempDir()

	r := NewRunner(dir)
	res := r.Run("echo hello")

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "hello" {
		t.Errorf("expected stdout 'hello', got %q", res.Stdout)
	}
}

func TestRunner_RunInProjectRoot(t *testing.T) {
	dir := t.TempDir()

	// Create a marker file to verify the command runs in the correct directory.
	marker := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(marker, []byte("found"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(dir)
	res := r.Run("cat marker.txt")

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "found" {
		t.Errorf("expected stdout 'found', got %q", res.Stdout)
	}
}

func TestRunner_RunFailingCommand(t *testing.T) {
	r := NewRunner(t.TempDir())
	res := r.Run("false")

	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit code")
	}
}

func TestRunner_RunRejectsBadCommand(t *testing.T) {
	r := NewRunner(t.TempDir())
	res := r.Run("echo hi\nrm -rf /")

	if res.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "rejected") {
		t.Errorf("expected rejection message, got %q", res.Stderr)
	}
}
