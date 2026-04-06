package multiplexer

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// TmuxMultiplexer implements Multiplexer via the tmux CLI.
type TmuxMultiplexer struct {
	sessionID string
	panes     map[PaneID]string // PaneID → tmux target string
	paneCount int
}

const splitPaneLimit = 4

// New creates a TmuxMultiplexer for the given session ID.
// Call CreateSession before using other methods.
func New(sessionID string) *TmuxMultiplexer {
	return &TmuxMultiplexer{
		sessionID: sessionID,
		panes:     make(map[PaneID]string),
	}
}

// CreateSession runs: tmux new-session -d -s <id> -x 220 -y 50
func (t *TmuxMultiplexer) CreateSession(id string) error {
	t.sessionID = id
	if err := t.tmux("new-session", "-d", "-s", id, "-x", "220", "-y", "50"); err != nil {
		return err
	}
	return t.tmux("rename-window", "-t", fmt.Sprintf("%s:0", id), "agents")
}

// NewPane creates tmux panes for the first 4 roles using splits in the initial
// "agents" window. Additional roles use dedicated windows so panes stay legible.
func (t *TmuxMultiplexer) NewPane(role bus.AgentRole) (PaneID, error) {
	var target string
	if t.paneCount == 0 {
		target = fmt.Sprintf("%s:0.0", t.sessionID)
	} else if t.paneCount < splitPaneLimit {
		paneID, err := t.tmuxOutput(
			"split-window", "-d",
			"-t", fmt.Sprintf("%s:0", t.sessionID),
			"-P", "-F", "#{pane_id}",
		)
		if err != nil {
			return "", fmt.Errorf("tmux split-window: %w", err)
		}
		target = strings.TrimSpace(paneID)
		if err := t.tmux("select-layout", "-t", fmt.Sprintf("%s:0", t.sessionID), "tiled"); err != nil {
			return "", fmt.Errorf("tmux select-layout tiled: %w", err)
		}
	} else {
		windowIdx := fmt.Sprintf("%d", t.paneCount-splitPaneLimit+1)
		if err := t.tmux("new-window", "-d", "-t", t.sessionID, "-n", string(role)); err != nil {
			return "", fmt.Errorf("tmux new-window: %w", err)
		}
		target = fmt.Sprintf("%s:%s.0", t.sessionID, windowIdx)
	}
	t.paneCount++
	id := PaneID(fmt.Sprintf("%s-%d", role, t.paneCount-1))
	t.panes[id] = target
	return id, nil
}

// WriteToPane sends text to the pane without pressing Enter.
func (t *TmuxMultiplexer) WriteToPane(id PaneID, text string) error {
	target, ok := t.panes[id]
	if !ok {
		return fmt.Errorf("unknown pane: %s", id)
	}
	return t.tmux("send-keys", "-t", target, "-l", text)
}

// SendCommand sends a shell command to the pane and presses Enter.
func (t *TmuxMultiplexer) SendCommand(id PaneID, cmd string) error {
	target, ok := t.panes[id]
	if !ok {
		return fmt.Errorf("unknown pane: %s", id)
	}
	if err := t.tmux("send-keys", "-t", target, "-l", cmd); err != nil {
		return err
	}
	return t.tmux("send-keys", "-t", target, "Enter")
}

// SpawnAgent sends the orchestrator agent sub-command to a pane.
func (t *TmuxMultiplexer) SpawnAgent(pane PaneID, role bus.AgentRole, sessionID, socketPath string) error {
	cmd := fmt.Sprintf("orchestrator agent --role=%s --session=%s --bus-addr=%s",
		role, sessionID, socketPath)
	return t.SendCommand(pane, cmd)
}

// Close kills the tmux session.
func (t *TmuxMultiplexer) Close() error {
	return t.tmux("kill-session", "-t", t.sessionID)
}

func (t *TmuxMultiplexer) tmux(args ...string) error {
	cmd := exec.Command("tmux", args...) //nolint:gosec // G204: args built internally for tmux session management
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

func (t *TmuxMultiplexer) tmuxOutput(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...) //nolint:gosec // G204: args built internally for tmux session management
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}
