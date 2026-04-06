package skills

import (
	"fmt"
	"sync"
)

// Loader provides skill content from embedded files.
// Skills are bundled into the binary at compile time — no external fetches.
type Loader struct {
	mu    sync.RWMutex
	cache map[string]string
}

// New creates a Loader backed by embedded skill files.
// The cacheDir parameter is kept for backward compatibility but ignored.
func New(_ string) *Loader {
	return &Loader{
		cache: make(map[string]string),
	}
}

// Load returns the content of a skill by name from embedded files.
func (l *Loader) Load(name string) (string, error) {
	l.mu.RLock()
	if content, ok := l.cache[name]; ok {
		l.mu.RUnlock()
		return content, nil
	}
	l.mu.RUnlock()

	data, err := embeddedSkills.ReadFile("embedded/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("skill %q not found: %w", name, err)
	}

	content := string(data)

	l.mu.Lock()
	l.cache[name] = content
	l.mu.Unlock()

	return content, nil
}

// Prefetch preloads a list of skills into the in-memory cache.
func (l *Loader) Prefetch(names []string) error {
	var firstErr error
	for _, name := range names {
		if _, err := l.Load(name); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Available returns the names of all embedded skills.
func (l *Loader) Available() []string {
	entries, err := embeddedSkills.ReadDir("embedded")
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
