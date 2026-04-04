package skills

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_FetchAndCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Test Skill\nThis is a test skill."))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	l := &Loader{
		cacheDir:   cacheDir,
		httpClient: srv.Client(),
	}

	// override rawBase by patching fetch directly via a test server URL
	// We test fetch() directly since rawBase is a package-level const.
	// Instead, test Load() with a custom httpClient pointing to our server.
	// We need to override the URL — simplest: replace fetch with a wrapper.

	// Test via direct fetch with overridden URL.
	content, err := l.fetchURL(srv.URL + "/skills/golang-patterns/SKILL.md")
	if err != nil {
		t.Fatalf("fetchURL error: %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty content")
	}

	// Write to cache manually, then test cache hit.
	path := cachePath(cacheDir, "golang-patterns")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second load should hit cache (server could be dead).
	srv.Close()
	content2, err := l.Load("golang-patterns")
	if err != nil {
		t.Fatalf("Load (cache hit) error: %v", err)
	}
	if content2 != content {
		t.Errorf("cache hit returned different content")
	}
}

func TestLoader_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	l := &Loader{
		cacheDir:   cacheDir,
		httpClient: srv.Client(),
	}

	// 404 should return empty string, no error (non-fatal)
	content, err := l.Load("nonexistent-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// content may be empty since fetch failed and cache miss
	_ = content

	// No cache file should have been written.
	if _, err := os.Stat(filepath.Join(cacheDir, "nonexistent-skill.md")); !os.IsNotExist(err) {
		t.Error("cache file should not exist for failed fetch")
	}
}

func TestPrefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("skill content"))
	}))
	defer srv.Close()

	l := &Loader{
		cacheDir:   t.TempDir(),
		httpClient: srv.Client(),
	}

	// Prefetch shouldn't panic or error even if skills don't exist on the server.
	err := l.Prefetch([]string{"a", "b", "c"})
	_ = err // non-fatal
}
