package executil

import (
	"context"
	"os/exec"
)

// Command wraps exec.Command for internal use where arguments are
// built programmatically from trusted sources (config values, constants).
// Centralises the gosec G204 suppression to a single location.
func Command(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...) // #nosec G204 -- callers pass internally-built args only
}

// CommandContext wraps exec.CommandContext for internal use where arguments
// are built programmatically from trusted sources.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...) // #nosec G204 -- callers pass internally-built args only
}
