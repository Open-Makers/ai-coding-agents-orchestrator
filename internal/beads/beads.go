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
// `bd q` (quick capture) which prints only the issue ID. labels are attached
// at creation time (may be empty).
func Create(ctx context.Context, root, title, description string, priority int, labels ...string) (string, error) {
	if !Available() {
		return "", fmt.Errorf("bd not installed")
	}
	args := []string{
		"q", title,
		"--description", description,
		"--type", "task",
		"--priority", fmt.Sprintf("%d", priority),
	}
	if len(labels) > 0 {
		args = append(args, "--labels", strings.Join(labels, ","))
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

// Link records a typed relationship between two issues, e.g. parent-child:
//
//	bd link <child> <parent> --type parent-child
//
// Best-effort: a no-op when bd is unavailable or an id is empty.
func Link(ctx context.Context, root, child, parent, linkType string) error {
	if !Available() || child == "" || parent == "" {
		return nil
	}
	if linkType == "" {
		linkType = "parent-child"
	}
	_, err := runCmd(ctx, root, "link", child, parent, "--type", linkType)
	return err
}

// SetMetadata attaches custom key/value metadata to an issue via
// `bd update <id> --set-metadata k=v`. Best-effort.
func SetMetadata(ctx context.Context, root, id string, kv map[string]string) error {
	if !Available() || id == "" || len(kv) == 0 {
		return nil
	}
	args := []string{"update", id}
	for k, v := range kv {
		args = append(args, "--set-metadata", k+"="+v)
	}
	_, err := runCmd(ctx, root, args...)
	return err
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

// Ready returns issues that are unblocked and ready to be picked up. When
// parent is non-empty, results are scoped to descendants of that bead/epic
// (`bd ready --parent`). Note: `bd ready` excludes in_progress issues, so an
// interrupted (claimed-but-open) sub-task will NOT appear here — callers that
// need to resume such work should use Children(..., "in_progress").
func Ready(ctx context.Context, root, parent string) ([]Issue, error) {
	if !Available() {
		return nil, nil
	}
	args := []string{"ready", "--json"}
	if parent != "" {
		args = append(args, "--parent", parent)
	}
	out, err := runCmd(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd ready: parse json: %w", err)
	}
	return issues, nil
}

// ActiveTasks lists top-level orchestrator task beads that are currently
// in_progress, i.e. resumable runs. Uses
// `bd list --no-parent --label orchestrator-task --status in_progress --json`.
// Returns nil when bd is unavailable.
func ActiveTasks(ctx context.Context, root, label string) ([]Issue, error) {
	if !Available() {
		return nil, nil
	}
	if label == "" {
		label = "orchestrator-task"
	}
	out, err := runCmd(ctx, root,
		"list", "--no-parent", "--label", label, "--status", "in_progress", "--json")
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd list active: parse json: %w", err)
	}
	return issues, nil
}

// ClosedTasks lists completed top-level orchestrator task beads (history). Uses
// `bd list --no-parent --label orchestrator-task --status closed --json`.
func ClosedTasks(ctx context.Context, root, label string) ([]Issue, error) {
	if !Available() {
		return nil, nil
	}
	if label == "" {
		label = "orchestrator-task"
	}
	out, err := runCmd(ctx, root,
		"list", "--no-parent", "--label", label, "--status", "closed", "--json")
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd list closed: parse json: %w", err)
	}
	return issues, nil
}

// (e.g. "in_progress", "open"). Returns nil when bd is unavailable or parent
// is empty.
func Children(ctx context.Context, root, parent string, statuses ...string) ([]Issue, error) {
	if !Available() || parent == "" {
		return nil, nil
	}
	args := []string{"list", "--parent", parent, "--json"}
	if len(statuses) > 0 {
		args = append(args, "--status", strings.Join(statuses, ","))
	}
	out, err := runCmd(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("bd list: parse json: %w", err)
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
