package multiplexer

import "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"

// PaneID identifies a terminal pane within a multiplexer session.
type PaneID string

// Multiplexer manages a terminal multiplexer session with per-agent panes.
type Multiplexer interface {
	// CreateSession creates a new detached session with the given ID.
	CreateSession(id string) error
	// NewPane creates a new pane (window or split) for the given agent role.
	NewPane(role bus.AgentRole) (PaneID, error)
	// WriteToPane sends text to the pane (does not press Enter).
	WriteToPane(id PaneID, text string) error
	// Close kills the entire session.
	Close() error
}
