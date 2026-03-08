package orchestrator

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Patcher applies diff patches to the repo.
type Patcher interface {
	Apply(ctx *Context, patchPath string) error
}

// GitPatcher applies patches using `git apply`.
type GitPatcher struct{}

func (p GitPatcher) Apply(ctx *Context, patchPath string) error {
	cmd := exec.Command("git", "apply", patchPath)
	cmd.Dir = ctx.Root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git apply failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
