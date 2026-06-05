package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Workspace memory directory layout (relative to .orchestrator/memory).
const (
	// MemoryFile holds persistent facts/decisions/preferences, loaded into
	// every agent prompt verbatim. Keep it small.
	MemoryFile = "MEMORY.md"
	// DreamsFile holds (optional) consolidated digests of past daily logs.
	DreamsFile = "DREAMS.md"
	// DailyDir is the directory for date-stamped event logs.
	DailyDir = "daily"
	// TasksDir is the directory for per-task summaries.
	TasksDir = "tasks"
)

// Layout describes where memory lives on disk for a single workspace.
type Layout struct {
	// Root is the absolute path to the memory directory (e.g.
	// "/path/to/repo/.orchestrator/memory").
	Root string
}

// NewLayout returns a Layout rooted at memDir and ensures all expected
// subdirectories exist.
func NewLayout(memDir string) (Layout, error) {
	if memDir == "" {
		return Layout{}, fmt.Errorf("memory: empty memDir")
	}
	abs, err := filepath.Abs(memDir)
	if err != nil {
		return Layout{}, fmt.Errorf("memory: abs: %w", err)
	}
	for _, sub := range []string{"", DailyDir, TasksDir} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0o750); err != nil {
			return Layout{}, fmt.Errorf("memory: mkdir %s: %w", sub, err)
		}
	}
	return Layout{Root: abs}, nil
}

// MemoryPath returns the absolute path of MEMORY.md.
func (l Layout) MemoryPath() string { return filepath.Join(l.Root, MemoryFile) }

// DreamsPath returns the absolute path of DREAMS.md.
func (l Layout) DreamsPath() string { return filepath.Join(l.Root, DreamsFile) }

// DailyPath returns the absolute path of the daily log file for the given date.
func (l Layout) DailyPath(date time.Time) string {
	return filepath.Join(l.Root, DailyDir, date.Format("2006-01-02")+".md")
}

// TaskPath returns the absolute path of the per-task summary file.
func (l Layout) TaskPath(taskID string) string {
	id := sanitiseFilename(taskID)
	if id == "" {
		id = "untitled"
	}
	return filepath.Join(l.Root, TasksDir, id+".md")
}

// AppendDaily appends a timestamped entry to today's daily log.
//
//	## HH:MM — <stage>
//
//	<body>
func (l Layout) AppendDaily(now time.Time, stage, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	path := l.DailyPath(now)
	header := fmt.Sprintf("\n## %s — %s\n\n", now.Format("15:04"), stage)
	if !fileExists(path) {
		header = fmt.Sprintf("# Daily log %s\n", now.Format("2006-01-02")) + header
	}
	return appendFile(path, header+strings.TrimRight(body, "\n")+"\n")
}

// WriteTaskSummary writes (or overwrites) the per-task summary file.
func (l Layout) WriteTaskSummary(taskID, title, body string) error {
	path := l.TaskPath(taskID)
	header := fmt.Sprintf("# %s\n\n_Task: %s_  \n_Recorded: %s_\n\n",
		strings.TrimSpace(title), taskID, time.Now().Format(time.RFC3339))
	return os.WriteFile(path, []byte(header+strings.TrimRight(body, "\n")+"\n"), 0o600) // #nosec G306
}

// ReadMemory returns the content of MEMORY.md, or "" if missing.
func (l Layout) ReadMemory() string {
	b, err := os.ReadFile(l.MemoryPath()) // #nosec G304 -- workspace-controlled path
	if err != nil {
		return ""
	}
	return string(b)
}

// MemoryFiles lists every memory file (relative to Root) currently on disk.
// Order is deterministic: MEMORY.md, DREAMS.md, daily/* sorted desc, tasks/* sorted.
func (l Layout) MemoryFiles() []string {
	var out []string
	if fileExists(l.MemoryPath()) {
		out = append(out, MemoryFile)
	}
	if fileExists(l.DreamsPath()) {
		out = append(out, DreamsFile)
	}
	out = append(out, listSubdir(l.Root, DailyDir, true)...)
	out = append(out, listSubdir(l.Root, TasksDir, false)...)
	return out
}

func listSubdir(root, sub string, desc bool) []string {
	entries, err := os.ReadDir(filepath.Join(root, sub))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, filepath.Join(sub, e.Name()))
	}
	sort.Slice(out, func(i, j int) bool {
		if desc {
			return out[i] > out[j]
		}
		return out[i] < out[j]
	})
	return out
}

// Fact represents a single line/bullet promoted to MEMORY.md.
type Fact struct {
	Source string // e.g. "architecture.md#Decisions"
	Text   string // single line of decision/fact
}

// PromoteFacts appends facts to MEMORY.md under a dated heading, skipping any
// fact whose sha256 hash is already present in the file. Returns the number
// of newly written facts.
func (l Layout) PromoteFacts(now time.Time, taskID string, facts []Fact) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}
	existing := l.ReadMemory()
	existingHashes := extractFactHashes(existing)

	var newFacts []Fact
	for _, f := range facts {
		text := strings.TrimSpace(f.Text)
		if text == "" {
			continue
		}
		h := factHash(text)
		if _, dup := existingHashes[h]; dup {
			continue
		}
		existingHashes[h] = struct{}{}
		newFacts = append(newFacts, Fact{Source: f.Source, Text: text})
	}
	if len(newFacts) == 0 {
		return 0, nil
	}

	var sb strings.Builder
	if existing == "" {
		sb.WriteString("# Project Memory\n\n")
		sb.WriteString("Persistent decisions and facts. Edit freely; never delete history.\n\n")
	} else if !strings.HasSuffix(existing, "\n") {
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "\n## %s — promoted from task %s\n\n",
		now.Format("2006-01-02"), taskID)
	for _, f := range newFacts {
		fmt.Fprintf(&sb, "- %s  <!-- src=%s hash=%s -->\n",
			f.Text, f.Source, factHash(f.Text))
	}
	return len(newFacts), appendFile(l.MemoryPath(), sb.String())
}

var factHashRe = regexp.MustCompile(`hash=([0-9a-f]{12})`)

func extractFactHashes(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range factHashRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

func factHash(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(s))))
	return hex.EncodeToString(sum[:6])
}

// ExtractDecisions parses a Markdown document and returns bullet items under
// any heading whose title contains one of the markers (case-insensitive).
// Bullet items are recognised as lines starting with "-", "*", or "1." etc.
func ExtractDecisions(doc string, markers []string) []string {
	if doc == "" {
		return nil
	}
	if len(markers) == 0 {
		markers = []string{"decision", "constraint", "principle"}
	}
	lower := func(s string) string { return strings.ToLower(s) }
	matches := func(h string) bool {
		hl := lower(h)
		for _, m := range markers {
			if strings.Contains(hl, lower(m)) {
				return true
			}
		}
		return false
	}

	lines := strings.Split(doc, "\n")
	var out []string
	inSection := false
	headingDepth := 0

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "#") {
			depth := 0
			for _, r := range trimmed {
				if r == '#' {
					depth++
				} else {
					break
				}
			}
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if matches(title) {
				inSection = true
				headingDepth = depth
				continue
			}
			if inSection && depth <= headingDepth {
				inSection = false
			}
			continue
		}
		if !inSection {
			continue
		}
		if isBullet(trimmed) {
			item := strings.TrimSpace(stripBulletPrefix(trimmed))
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func isBullet(s string) bool {
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") {
		return true
	}
	// numbered list "1." / "2)"
	for i, r := range s {
		if r >= '0' && r <= '9' {
			continue
		}
		if (r == '.' || r == ')') && i > 0 && i+1 < len(s) && s[i+1] == ' ' {
			return true
		}
		break
	}
	return false
}

func stripBulletPrefix(s string) string {
	for i, r := range s {
		if r == '-' || r == '*' || (r >= '0' && r <= '9') || r == '.' || r == ')' {
			continue
		}
		if r == ' ' {
			return s[i+1:]
		}
		return s
	}
	return s
}

func appendFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- workspace path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Size() > 0
}

var safeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitiseFilename(s string) string {
	s = strings.TrimSpace(s)
	s = safeFilenameRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
