package agent

import (
	"os/exec"
	"regexp"
	"strings"
)

// goVersionRegex matches the version token in `go version` output, e.g.
// "go version go1.26.0 darwin/arm64" → "go1.26.0".
var goVersionRegex = regexp.MustCompile(`go\d+\.\d+(?:\.\d+)?`)

// detectGoToolchain returns the version reported by the locally installed
// `go` binary (e.g. "go1.26.0"). Returns "" if Go is not installed or the
// command fails.
func detectGoToolchain() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return ""
	}
	return goVersionRegex.FindString(strings.TrimSpace(string(out)))
}
