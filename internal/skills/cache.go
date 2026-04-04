package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

func defaultCacheDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "orchestrator", "skills")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".cache", "orchestrator", "skills")
	}
	return filepath.Join(home, ".config", "orchestrator", "skills")
}

func ensureCacheDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("skills: create cache dir %s: %w", dir, err)
	}
	return nil
}

func cachePath(dir, name string) string {
	return filepath.Join(dir, name+".md")
}
