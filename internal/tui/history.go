package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

const historyFile = "history.json"
const historyMax = 10

// LoadHistory reads the list of recently used requirements paths.
func LoadHistory(wsPath string) []string {
	data, err := safefile.ReadFile(wsPath, historyFile)
	if err != nil {
		return nil
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return nil
	}
	return paths
}

// SaveHistory prepends path to the history, deduplicates, trims to max.
func SaveHistory(wsPath, path string) error {
	existing := LoadHistory(wsPath)

	// deduplicate
	seen := map[string]bool{path: true}
	deduped := []string{path}
	for _, p := range existing {
		if !seen[p] {
			seen[p] = true
			deduped = append(deduped, p)
		}
	}
	if len(deduped) > historyMax {
		deduped = deduped[:historyMax]
	}

	data, err := json.MarshalIndent(deduped, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(wsPath, historyFile), data, 0o600)
}
