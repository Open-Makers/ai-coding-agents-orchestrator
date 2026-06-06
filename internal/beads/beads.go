// Package beads is a thin wrapper around the `bd` CLI from
// github.com/gastownhall/beads. It is used by the PM/TaskRunner to register
// negotiated task specs as durable issues so multi-session work survives.
//
// All operations are best-effort: if the binary is missing or the command
// fails, callers should log and continue rather than abort the pipeline.
package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Issue is the minimal subset of `bd` issue fields the orchestrator cares about.
type Issue struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    int    `json:"priority"`
	Type        string `json:"type"`
}

// Available reports whether the `bd` binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("bd")
	return err == nil
}

// Create registers a new issue and returns its ID. Output is parsed from
// `bd q` (quick capture) which prints only the issue ID.
func Create(ctx context.Context, root, title, description string, priority int) (string, error) {
	if !Available() {
		return "", fmt.Errorf("bd not installed")
	}
	args := []string{
		"q", title,
		"--description", description,
		"--type", "task",
		"--priority", fmt.Sprintf("%d", priority),
	}
	out, err := runCmd(ctx, root, args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("bd create: empty id")
	}
	return id, nil
}

// Claim transitions an issue to in-progress for the current actor.
func Claim(ctx context.Context, root, id string) error {
	if !Available() || id == "" {
		return nil
	}
	_, err := runCmd(ctx, root, "update", id, "--claim")
	return err
}

// AddDependency records that `child` depends on `parent` — parent must be closed
// before `child` becomes ready. Implemented via `bd dep add <child> <parent>`,
// which is the standard subcommand of the beads CLI.
func AddDependency(ctx context.Context, root, child, parent string) error {
	if !Available() || child == "" || parent == "" {
		return nil
	}
	_, err := runCmd(ctx, root, "dep", "add", child, parent)
	return err
}

// Close marks an issue as done with the given reason.
func Close(ctx context.Context, root, id, reason string) error {
	if !Available() || id == "" {
		return nil
	}
	if reason == "" {
		reason = "Completed by orchestrator"
	}
	_, err := runCmd(ctx, root, "close", id, "--reason", reason)
	return err
}

// Ready returns issues that are unblocked and ready to be picked up.
func Ready(ctx context.Context, root string) ([]Issue, error) {
	if !Available() {
		return nil, nil
	}
	out, err := runCmd(ctx, root, "ready", "--json")
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd ready: parse json: %w", err)
	}
	return issues, nil
}

func runCmd(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bd", args...) // #nosec G204 -- "bd" is a constant; args are internally built, not user-derived
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("bd %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
