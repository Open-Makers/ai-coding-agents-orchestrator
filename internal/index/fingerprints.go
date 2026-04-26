package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

// FileFingerprint is a single (path, sha256) entry. The SHA-256 is computed
// over the file's verbatim bytes.
type FileFingerprint struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// BuildFingerprints returns a path → sha256 map for the given repo-relative files.
// Files unreadable from root are skipped silently.
func BuildFingerprints(root string, files []string) (map[string]string, error) {
	out := make(map[string]string, len(files))
	for _, f := range files {
		data, err := safefile.ReadFile(root, f)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out[f] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// DiffFingerprints classifies paths between two snapshots.
func DiffFingerprints(prev, curr map[string]string) (added, modified, deleted []string) {
	for p, hash := range curr {
		old, ok := prev[p]
		switch {
		case !ok:
			added = append(added, p)
		case old != hash:
			modified = append(modified, p)
		}
	}
	for p := range prev {
		if _, ok := curr[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	return added, modified, deleted
}

// SaveFingerprints persists a snapshot to path (creating parent dirs as needed).
func SaveFingerprints(path string, fp map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(fp)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadFingerprints reads a snapshot from path. A missing file returns
// an empty map without error so first-run callers can treat it uniformly.
func LoadFingerprints(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- internal path under .orchestrator/
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
