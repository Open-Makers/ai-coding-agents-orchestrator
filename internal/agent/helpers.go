package agent

import (
	"bufio"
	"os"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
)

// TestReport is the outcome of running a project's test suite.
type TestReport struct {
	Success  bool              `json:"success"`
	Commands []executil.Result `json:"commands"`
}

// parseSections extracts delimited sections from LLM output.
// Recognises multiple delimiter styles:
//   - ===KEY===
//   - ### KEY ===  /  ### KEY
//   - ## KEY
//   - **KEY**
func parseSections(output string, keys ...string) map[string]string {
	result := make(map[string]string, len(keys))
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[strings.ToUpper(k)] = true
	}

	current := ""
	for _, line := range strings.Split(output, "\n") {
		if name := extractSectionName(line, keySet); name != "" {
			current = name
			continue
		}
		if current != "" {
			result[current] += line + "\n"
		}
	}

	for k := range result {
		result[k] = strings.TrimSpace(result[k])
	}
	return result
}

// extractSectionName tries to extract a known section key from a delimiter line.
func extractSectionName(line string, keySet map[string]bool) string {
	trim := strings.TrimSpace(line)
	if trim == "" {
		return ""
	}

	hasDecorator := strings.HasPrefix(trim, "=") || strings.HasPrefix(trim, "#") ||
		strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "_")

	cleaned := strings.Trim(trim, "=#*_ \t")
	cleaned = strings.TrimSpace(cleaned)
	candidate := strings.ToUpper(cleaned)

	if keySet[candidate] {
		return candidate
	}

	if !hasDecorator {
		return ""
	}

	for key := range keySet {
		if strings.HasPrefix(candidate, key) {
			tail := candidate[len(key):]
			if tail == "" || tail[0] == ' ' || tail[0] == '(' || tail[0] == '-' || tail[0] == ':' {
				return key
			}
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseCmds(input string) []string {
	var cmds []string
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		cleaned := stripListMarker(line)
		cleaned = strings.Trim(cleaned, "`")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			continue
		}
		if !looksLikeCommand(cleaned) {
			continue
		}
		cmds = append(cmds, cleaned)
	}
	return cmds
}

func stripListMarker(s string) string {
	if len(s) > 2 && (s[0] == '-' || s[0] == '*') && s[1] == ' ' {
		return strings.TrimSpace(s[2:])
	}
	for i, c := range s {
		if c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == ')') && i+1 < len(s) && s[i+1] == ' ' {
			return strings.TrimSpace(s[i+2:])
		}
		break
	}
	return s
}

func looksLikeCommand(line string) bool {
	cmdPrefixes := []string{
		"go ", "make", "npm ", "yarn ", "pnpm ", "cargo ", "python ", "pip ",
		"pytest", "ruby ", "bundle ", "mvn ", "gradle ", "docker ", "kubectl ",
		"curl ", "wget ", "cat ", "echo ", "ls ", "cd ", "mkdir ", "rm ",
		"cp ", "mv ", "chmod ", "sh ", "bash ", "test ", "./", "set ",
	}
	lower := strings.ToLower(line)
	for _, p := range cmdPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return strings.HasPrefix(line, "$ ")
}

// isInteractiveCommand returns true for commands that may require user input
// or run indefinitely (e.g. go run, servers, REPLs).
func isInteractiveCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))

	interactivePrefixes := []string{
		"go run ",
		"python -c ",
		"node -e ",
		"npm start",
		"yarn start",
		"npm run dev",
		"yarn dev",
	}
	for _, prefix := range interactivePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	if strings.Contains(lower, "| go run") || strings.Contains(lower, "|go run") {
		return true
	}

	return false
}
