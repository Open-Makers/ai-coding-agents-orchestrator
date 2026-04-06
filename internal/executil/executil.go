package executil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxOutputBytes caps captured stdout/stderr per command.
const MaxOutputBytes = 1 << 20 // 1 MiB

// maxCmdLen limits command string length to prevent abuse.
const maxCmdLen = 512

// DefaultTimeout is the maximum wall-clock time a single command may run.
const DefaultTimeout = 120 * time.Second

// Result holds the outcome of a single shell command.
type Result struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// Runner executes shell commands scoped to a specific project root directory.
// Commands are always run with sh -c inside the configured root.
type Runner struct {
	root    string
	timeout time.Duration
}

// NewRunner creates a command runner bound to the given project root.
func NewRunner(root string) *Runner {
	return &Runner{root: root, timeout: DefaultTimeout}
}

// WithTimeout returns a copy of the runner with a custom timeout.
func (r *Runner) WithTimeout(d time.Duration) *Runner {
	return &Runner{root: r.root, timeout: d}
}

// Run executes a shell command in the project root directory.
// The command is validated before execution: it must not be empty,
// contain embedded newlines, control characters, or exceed the length limit.
// Commands are killed after the configured timeout (default 120s).
func (r *Runner) Run(command string) Result {
	if err := Sanitize(command); err != nil {
		return Result{Command: command, ExitCode: 1, Stderr: "rejected: " + err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = r.root

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdoutBuf, remaining: MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderrBuf, remaining: MaxOutputBytes}

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		exitCode = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = 124 // same as timeout(1)
			fmt.Fprintf(&stderrBuf, "\ncommand timed out after %s", r.timeout)
		}
	}

	return Result{
		Command:  command,
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(stdoutBuf.String()),
		Stderr:   strings.TrimSpace(stderrBuf.String()),
	}
}

// RunUnchecked executes a command without sanitization. Use only for
// known-safe internal commands (e.g. go mod init with a validated path).
func (r *Runner) RunUnchecked(command string) Result {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = r.root

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdoutBuf, remaining: MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderrBuf, remaining: MaxOutputBytes}

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		exitCode = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = 124
			fmt.Fprintf(&stderrBuf, "\ncommand timed out after %s", r.timeout)
		}
	}

	return Result{
		Command:  command,
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(stdoutBuf.String()),
		Stderr:   strings.TrimSpace(stderrBuf.String()),
	}
}

// Sanitize rejects commands that are empty, too long, contain embedded
// newlines, or non-printable control characters.
func Sanitize(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("empty command")
	}
	if utf8.RuneCountInString(command) > maxCmdLen {
		return fmt.Errorf("command too long (%d runes, max %d)", utf8.RuneCountInString(command), maxCmdLen)
	}
	for i, r := range command {
		if r == '\n' || r == '\r' {
			return fmt.Errorf("command contains embedded newline at position %d", i)
		}
		if r < 0x20 && r != '\t' {
			return fmt.Errorf("command contains control character 0x%02x", r)
		}
	}
	return nil
}

// limitedWriter wraps a bytes.Buffer and stops writing after a byte cap.
type limitedWriter struct {
	buf       *bytes.Buffer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return len(p), nil
	}
	n := int64(len(p))
	if n > w.remaining {
		p = p[:w.remaining]
	}
	written, err := w.buf.Write(p)
	w.remaining -= int64(written)
	if err != nil {
		return written, err
	}
	return int(n), nil
}
