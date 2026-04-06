package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const horizontalScrollStep = 4

// horizontalSlice shifts each line of content left by offsetX visible characters
// and truncates to viewWidth. ANSI escape sequences are handled correctly.
func horizontalSlice(content string, offsetX, viewWidth int) string {
	if offsetX <= 0 && viewWidth <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if offsetX > 0 {
			line = ansi.TruncateLeft(line, offsetX, "")
		}
		if viewWidth > 0 {
			line = ansi.Truncate(line, viewWidth, "")
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// maxLineWidth returns the maximum visible width among all lines in content.
func maxLineWidth(content string) int {
	maxW := 0
	for _, line := range strings.Split(content, "\n") {
		w := ansi.StringWidth(line)
		if w > maxW {
			maxW = w
		}
	}
	return maxW
}
