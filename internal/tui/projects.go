package tui

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

const projectsFile = "projects.json"
const maxRecentProjects = 20

// RecentProject stores metadata about a previously opened project.
type RecentProject struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	LastUsed string `json:"last_used"`
}

func projectsFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, config.GlobalDir, projectsFile)
}

// LoadRecentProjects reads the list of recently opened projects from
// ~/.orchestrator/projects.json. The user's home directory and missing paths
// are excluded from the returned list.
func LoadRecentProjects() []RecentProject {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	// Use DirFS to safely scope file access within home directory
	homeFS := os.DirFS(home)
	data, err := fs.ReadFile(homeFS, filepath.Join(config.GlobalDir, projectsFile))
	if err != nil {
		return nil
	}
	var projects []RecentProject
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil
	}
	return filterRecentProjects(projects)
}

// SaveRecentProject adds or bumps a project to the top of the recent list.
// The user's home directory is never saved.
func SaveRecentProject(projectPath string) error {
	if isHomeDir(projectPath) {
		return nil
	}

	filePath := projectsFilePath()
	if filePath == "" {
		return nil
	}

	name := filepath.Base(projectPath)
	now := time.Now().Format(time.RFC3339)

	existing := LoadRecentProjects()
	updated := []RecentProject{{Path: projectPath, Name: name, LastUsed: now}}

	for _, p := range existing {
		if p.Path != projectPath {
			updated = append(updated, p)
		}
	}

	if len(updated) > maxRecentProjects {
		updated = updated[:maxRecentProjects]
	}

	data, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o600)
}

// RemoveRecentProject deletes a project from the recent list.
func RemoveRecentProject(projectPath string) error {
	filePath := projectsFilePath()
	if filePath == "" {
		return nil
	}

	existing := LoadRecentProjects()
	var filtered []RecentProject
	for _, p := range existing {
		if p.Path != projectPath {
			filtered = append(filtered, p)
		}
	}

	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o600)
}

// isHomeDir returns true when path is the user's home directory.
func isHomeDir(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return filepath.Clean(path) == filepath.Clean(home)
}

// filterRecentProjects removes entries that should never be shown in recents.
func filterRecentProjects(projects []RecentProject) []RecentProject {
	home, err := os.UserHomeDir()
	if err != nil {
		return projects
	}
	cleanHome := filepath.Clean(home)
	var filtered []RecentProject
	for _, p := range projects {
		cleanPath := filepath.Clean(p.Path)
		if cleanPath == cleanHome {
			continue
		}
		if _, err := os.Stat(cleanPath); err != nil {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}
