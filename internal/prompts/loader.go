package prompts

import (
	"fmt"
	"sync"
)

// Loader provides system prompt templates from embedded files.
// Prompts are bundled into the binary at compile time.
type Loader struct {
	mu    sync.RWMutex
	cache map[string]string
}

// New creates a Loader backed by embedded prompt files.
func New() *Loader {
	return &Loader{
		cache: make(map[string]string),
	}
}

// Load returns the content of a prompt template by name from embedded files.
func (l *Loader) Load(name string) (string, error) {
	l.mu.RLock()
	if content, ok := l.cache[name]; ok {
		l.mu.RUnlock()
		return content, nil
	}
	l.mu.RUnlock()

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
