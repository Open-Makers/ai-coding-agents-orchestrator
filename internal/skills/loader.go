package skills

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	rawBase = "https://raw.githubusercontent.com/affaan-m/everything-claude-code/main/skills/%s/SKILL.md"
	timeout = 10 * time.Second
)

// Loader fetches and caches skill files from GitHub.
type Loader struct {
	cacheDir   string
	httpClient *http.Client
}

// New creates a Loader. If cacheDir is empty the XDG default is used.
func New(cacheDir string) *Loader {
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	return &Loader{
		cacheDir:   cacheDir,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Load returns the content of a skill by name.
// Order: local cache → GitHub download → empty string (never blocks the caller on failure).
func (l *Loader) Load(name string) (string, error) {
	if err := ensureCacheDir(l.cacheDir); err != nil {
		return "", err
	}

	path := cachePath(l.cacheDir, name)

	// cache hit
	if data, err := os.ReadFile(path); err == nil {
		return string(data), nil
	}

	// fetch from GitHub
	content, err := l.fetch(name)
	if err != nil {
		// non-fatal: caller continues without this skill
		return "", nil //nolint:nilerr
	}

	// persist to cache (best-effort)
	_ = os.WriteFile(path, []byte(content), 0o644)

	return content, nil
}

// Prefetch downloads a list of skills concurrently and caches them.
func (l *Loader) Prefetch(names []string) error {
	var wg sync.WaitGroup
	errs := make(chan error, len(names))

	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if _, err := l.Load(n); err != nil {
				errs <- fmt.Errorf("prefetch %s: %w", n, err)
			}
		}(name)
	}

	wg.Wait()
	close(errs)

	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
	}
	return first
}

func (l *Loader) fetch(name string) (string, error) {
	return l.fetchURL(fmt.Sprintf(rawBase, name))
}

func (l *Loader) fetchURL(url string) (string, error) {
	resp, err := l.httpClient.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("skills: get %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("skills: HTTP %d for %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("skills: read body: %w", err)
	}
	return string(data), nil
}
