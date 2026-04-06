package tui

import (
	"encoding/json"
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

// LoadRecentProjects reads the list of recently opened projects from ~/.orchestrator/projects.json.
func LoadRecentProjects() []RecentProject {
	path := projectsFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var projects []RecentProject
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil
	}
	return projects
}

// SaveRecentProject adds or bumps a project to the top of the recent list.
func SaveRecentProject(projectPath string) error {
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o644)
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
	return os.WriteFile(filePath, data, 0o644)
}
