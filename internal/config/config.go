package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/safefile"
)

const Filename = ".orchestrator.yaml" // legacy project config (root-level)

// GlobalDir is the directory for user-wide settings.
const GlobalDir = ".orchestrator"

// GlobalFilename is the config file inside the global directory.
const GlobalFilename = "config.yaml"

// ProjectFilename is the project config file inside the workspace directory.
const ProjectFilename = "project.yaml"

type Config struct {
	Project        ProjectConfig          `yaml:"project,omitempty"`
	Agents         map[string]AgentConfig `yaml:"agents,omitempty"`
	PromptLanguage string                 `yaml:"prompt_language,omitempty"`
}

// SupportedLanguages lists languages available for LLM prompt responses.
var SupportedLanguages = []string{
	"English",
	"Polish",
	"German",
	"French",
	"Spanish",
	"Portuguese",
	"Italian",
	"Dutch",
	"Ukrainian",
	"Czech",
	"Swedish",
	"Norwegian",
	"Danish",
	"Finnish",
	"Turkish",
	"Japanese",
	"Korean",
	"Chinese",
	"Hindi",
	"Arabic",
	"Russian",
}

type ProjectConfig struct {
	Name           string        `yaml:"name,omitempty"`
	Language       string        `yaml:"language,omitempty"`
	ModulePath     string        `yaml:"module_path,omitempty"`
	TestCmd        string        `yaml:"test_cmd,omitempty"`
	LintCmd        string        `yaml:"lint_cmd,omitempty"`
	MaxFixAttempts int           `yaml:"max_fix_attempts,omitempty"`
	Scope          ScopeConfig   `yaml:"scope,omitempty"`
	Context        ContextConfig `yaml:"context,omitempty"`
}

type ScopeConfig struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

type ContextConfig struct {
	AlwaysInclude []string `yaml:"always_include,omitempty"`
}

type AgentConfig struct {
	Runner string   `yaml:"runner,omitempty"`
	Model  string   `yaml:"model,omitempty"`
	Skills []string `yaml:"-"` // always from defaults, never persisted
}

// globalConfigDir returns the directory for the global config (~/.orchestrator).
func globalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, GlobalDir)
}

// EnsureGlobalDir creates ~/.orchestrator if it doesn't exist.
func EnsureGlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// Save writes cfg to <root>/.orchestrator/project.yaml.
func Save(root string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, GlobalDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ProjectFilename), data, 0o600)
}

// SaveGlobal writes cfg to ~/.orchestrator/config.yaml.
func SaveGlobal(cfg Config) error {
	dir, err := EnsureGlobalDir()
	if err != nil {
		return err
	}
	// Merge with existing global config to preserve other settings.
	existing := Config{}
	if gDir := globalConfigDir(); gDir != "" {
		if loaded, loadErr := loadFile(gDir, GlobalFilename); loadErr == nil {
			existing = loaded
		}
	}
	merge(&existing, cfg)
	data, err := yaml.Marshal(existing)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, GlobalFilename), data, 0o600)
}

// Load reads config with layered precedence:
// 1. Built-in defaults
// 2. ~/.orchestrator/config.yaml (global user settings — models, runners, skills)
// 3. <root>/.orchestrator/project.yaml (project-specific overrides)
func Load(root string) (Config, error) {
	cfg := DefaultConfig()

	// Layer 2: global user config.
	if gDir := globalConfigDir(); gDir != "" {
		if global, err := loadFile(gDir, GlobalFilename); err == nil {
			merge(&cfg, global)
		}
	}

	// Layer 3: project-level config.
	projectDir := filepath.Join(root, GlobalDir)
	if project, err := loadFile(projectDir, ProjectFilename); err == nil {
		merge(&cfg, project)
	} else if errors.Is(err, os.ErrNotExist) {
		// Fallback: try legacy root-level config and auto-migrate.
		if project, legacyErr := loadFile(root, Filename); legacyErr == nil {
			merge(&cfg, project)
			_ = Save(root, project)                      // migrate to new location
			_ = os.Remove(filepath.Join(root, Filename)) // remove legacy file
		}
	} else {
		return cfg, err
	}

	return cfg, nil
}

// LoadProject reads only the project-level config (<root>/.orchestrator/project.yaml)
// without layering defaults or global settings. Returns an empty config if
// the file does not exist.
func LoadProject(root string) Config {
	cfg, err := loadFile(filepath.Join(root, GlobalDir), ProjectFilename)
	if err != nil {
		return Config{}
	}
	return cfg
}

// loadFile reads and unmarshals a single YAML config file scoped to dir.
func loadFile(dir, filename string) (Config, error) {
	data, err := safefile.ReadFile(dir, filename)
	if err != nil {
		return Config{}, err
	}
	var loaded Config
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return Config{}, err
	}
	return loaded, nil
}

// merge fills dst with non-zero values from src.
func merge(dst *Config, src Config) {
	if src.Project.Name != "" {
		dst.Project.Name = src.Project.Name
	}
	if src.Project.Language != "" {
		dst.Project.Language = src.Project.Language
	}
	if src.Project.ModulePath != "" {
		dst.Project.ModulePath = src.Project.ModulePath
	}
	if src.Project.TestCmd != "" {
		dst.Project.TestCmd = src.Project.TestCmd
	}
	if src.Project.LintCmd != "" {
		dst.Project.LintCmd = src.Project.LintCmd
	}
	if src.Project.MaxFixAttempts > 0 {
		dst.Project.MaxFixAttempts = src.Project.MaxFixAttempts
	}
	if len(src.Project.Scope.Allow) > 0 {
		dst.Project.Scope.Allow = src.Project.Scope.Allow
	}
	if len(src.Project.Scope.Deny) > 0 {
		dst.Project.Scope.Deny = src.Project.Scope.Deny
	}
	if len(src.Project.Context.AlwaysInclude) > 0 {
		dst.Project.Context.AlwaysInclude = src.Project.Context.AlwaysInclude
	}
	if src.PromptLanguage != "" {
		dst.PromptLanguage = src.PromptLanguage
	}

	if src.Agents != nil {
		if dst.Agents == nil {
			dst.Agents = make(map[string]AgentConfig)
		}
		for role, ac := range src.Agents {
			existing, ok := dst.Agents[role]
			if !ok {
				dst.Agents[role] = ac
				continue
			}
			if ac.Runner != "" {
				existing.Runner = ac.Runner
			}
			if ac.Model != "" {
				existing.Model = ac.Model
			}
			dst.Agents[role] = existing
		}
	}
}
