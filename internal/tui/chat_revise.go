package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// chatRevisionDoneMsg signals that an artifact revision completed.
type chatRevisionDoneMsg struct {
	artifact string
	err      error
}

// reviseArtifactCmd runs the revision in a goroutine and returns the result.
func reviseArtifactCmd(reviseFn ReviseFunc, artifact, feedback string) tea.Cmd {
	return func() tea.Msg {
		err := reviseFn(artifact, feedback)
		return chatRevisionDoneMsg{artifact: artifact, err: err}
	}
}

// parseReviseDirective extracts an artifact name and feedback from a PM response
// containing ===REVISE: artifact_name=== ... ===END=== block.
func parseReviseDirective(response string) (artifact, feedback string) {
	const revisePrefix = "===REVISE:"
	const reviseSuffix = "==="
	const endMarker = "===END==="

	idx := strings.Index(response, revisePrefix)
	if idx < 0 {
		return "", ""
	}

	afterPrefix := response[idx+len(revisePrefix):]
	suffixIdx := strings.Index(afterPrefix, reviseSuffix)
	if suffixIdx < 0 {
		return "", ""
	}

	artifact = strings.TrimSpace(afterPrefix[:suffixIdx])
	if artifact == "" {
		return "", ""
	}

	// Validate artifact name.
	validArtifacts := map[string]bool{
		"vision.md":              true,
		"moscow.md":              true,
		"architecture.md":        true,
		"implementation_plan.md": true,
		"prompts.md":             true,
	}
	if !validArtifacts[artifact] {
		return "", ""
	}

	rest := afterPrefix[suffixIdx+len(reviseSuffix):]
	endIdx := strings.Index(rest, endMarker)
	if endIdx >= 0 {
		feedback = strings.TrimSpace(rest[:endIdx])
	} else {
		feedback = strings.TrimSpace(rest)
	}

	return artifact, feedback
}
