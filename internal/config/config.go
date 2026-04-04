package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const Filename = ".orchestrator.yaml"

type Config struct {
	Project ProjectConfig            `yaml:"project"`
	Agents  map[string]AgentConfig   `yaml:"agents"`
}

type ProjectConfig struct {
	Name     string        `yaml:"name"`
	Language string        `yaml:"language"`
	TestCmd  string        `yaml:"test_cmd"`
	LintCmd  string        `yaml:"lint_cmd"`
	Scope    ScopeConfig   `yaml:"scope"`
	Context  ContextConfig `yaml:"context"`
}

type ScopeConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type ContextConfig struct {
	AlwaysInclude []string `yaml:"always_include"`
}

type AgentConfig struct {
	Runner string   `yaml:"runner"`
	Model  string   `yaml:"model"`
	Skills []string `yaml:"skills"`
}

// Load reads .orchestrator.yaml from root. Missing file returns defaults without error.
// Present file is merged with defaults (missing fields filled from defaults).
func Load(root string) (Config, error) {
	cfg := DefaultConfig()

	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}

	var loaded Config
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return cfg, err
	}

	merge(&cfg, loaded)
	return cfg, nil
}

// merge fills dst with non-zero values from src.
func merge(dst *Config, src Config) {
	if src.Project.Name != "" {
		dst.Project.Name = src.Project.Name
	}
	if src.Project.Language != "" {
		dst.Project.Language = src.Project.Language
	}
	if src.Project.TestCmd != "" {
		dst.Project.TestCmd = src.Project.TestCmd
	}
	if src.Project.LintCmd != "" {
		dst.Project.LintCmd = src.Project.LintCmd
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

	if src.Agents != nil {
		if dst.Agents == nil {
			dst.Agents = make(map[string]AgentConfig)
		}
		for role, ac := range src.Agents {
			dst.Agents[role] = ac
		}
	}
}
