package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

// PromptsDirName is the subdirectory within .orchestrator for custom prompts.
const PromptsDirName = "prompts"

// Loader provides system prompt templates from embedded files,
// with optional project-level overrides from .orchestrator/prompts/.
type Loader struct {
	mu       sync.RWMutex
	cache    map[string]string
	override string // project override directory (empty = no overrides)
}

// New creates a Loader backed by embedded prompt files.
func New() *Loader {
	return &Loader{
		cache: make(map[string]string),
	}
}

// SetOverrideDir configures a project-level directory to check for prompt overrides.
// Files in this directory take precedence over embedded defaults.
func SetOverrideDir(dir string) {
	defaultLoader.mu.Lock()
	defaultLoader.override = dir
	defaultLoader.cache = make(map[string]string) // clear cache to pick up overrides
	defaultLoader.mu.Unlock()
}

// Load returns the content of a prompt template by name.
// It checks project-level overrides first, then falls back to embedded.
func (l *Loader) Load(name string) (string, error) {
	l.mu.RLock()
	if content, ok := l.cache[name]; ok {
		l.mu.RUnlock()
		return content, nil
	}
	l.mu.RUnlock()

	// Check project-level override first.
	if l.override != "" {
		if data, err := safefile.ReadFile(l.override, name+".md"); err == nil {
			content := string(data)
			l.mu.Lock()
			l.cache[name] = content
			l.mu.Unlock()
			return content, nil
		}
	}

	// Fall back to embedded.
	data, err := embeddedPrompts.ReadFile("embedded/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("prompt %q not found: %w", name, err)
	}

	content := string(data)

	l.mu.Lock()
	l.cache[name] = content
	l.mu.Unlock()

	return content, nil
}

// MustLoad returns the content of a prompt or panics if not found.
func (l *Loader) MustLoad(name string) string {
	content, err := l.Load(name)
	if err != nil {
		panic(err)
	}
	return content
}

// Available returns the names of all embedded prompts.
func (l *Loader) Available() []string {
	entries, err := embeddedPrompts.ReadDir("embedded")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 3 && name[len(name)-3:] == ".md" {
			names = append(names, name[:len(name)-3])
		}
	}
	return names
}

// agentPromptMap maps agent roles to their primary prompt template names.
var agentPromptMap = map[string][]string{
	"pm":          {"pm-system"},
	"planner":     {"planner-system"},
	"coder":       {"coder-initial", "coder-fix", "coder-build-fix"},
	"tester":      {"tester-generate"},
	"reviewer":    {"reviewer-system"},
	"ux_reviewer": {"ux-reviewer-system"},
	"security":    {"security-system"},
	"qa":          {"qa-system"},
}

// PromptsForRole returns the prompt template names used by a given agent role.
func PromptsForRole(role string) []string {
	return agentPromptMap[role]
}

// ExportPrompt writes the default embedded prompt to a project-level override file.
// Returns the path of the written file.
func ExportPrompt(promptName, destDir string) (string, error) {
	data, err := embeddedPrompts.ReadFile("embedded/" + promptName + ".md")
	if err != nil {
		return "", fmt.Errorf("prompt %q not found: %w", promptName, err)
	}

	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return "", fmt.Errorf("create prompts dir: %w", err)
	}

	path := filepath.Join(destDir, promptName+".md")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}

	return path, nil
}

// OverrideExists returns true if a project-level override exists for the given prompt.
func OverrideExists(promptName, overrideDir string) bool {
	if overrideDir == "" {
		return false
	}
	info, err := safefile.Stat(overrideDir, promptName+".md")
	return err == nil && !info.IsDir()
}
