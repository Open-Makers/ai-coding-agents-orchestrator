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

// SupportedProgrammingLanguages lists selectable project programming languages.
var SupportedProgrammingLanguages = []string{
	"go",
	"rust",
	"python",
	"javascript",
	"typescript",
	"java",
	"ruby",
	"elixir",
	"c",
	"cpp",
	"csharp",
	"swift",
	"kotlin",
	"scala",
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
	AlwaysInclude   []string            `yaml:"always_include,omitempty"`
	ExcludePatterns []string            `yaml:"exclude_patterns,omitempty"`
	SemanticIndex   SemanticIndexConfig `yaml:"semantic_index,omitempty"`
	Memory          MemoryConfig        `yaml:"memory,omitempty"`

	// ScopedContextShadow enables measurement-only logging of symbol-scoped
	// source context during review: the scoped context is rendered and its
	// token estimate logged (agent="<role>-shadow") alongside the whole-file
	// context that is actually sent. It does NOT change what agents receive.
	// Requires the semantic index to be enabled. Default false.
	ScopedContextShadow bool `yaml:"scoped_context_shadow,omitempty"`

	// ScopedContextCoderFix enables ADDITIVE symbol-scoped related context in
	// the coder fix loop: the changed/error files are still rendered whole, and
	// semantically related declarations are appended scoped (cheaply) to improve
	// fix recall. Non-destructive — it never removes the code being fixed.
	// Requires the semantic index to be enabled. Default false.
	ScopedContextCoderFix bool `yaml:"scoped_context_coder_fix,omitempty"`
}

// SemanticIndexConfig controls the optional embeddings-based file index.
// Disabled by default; when enabled it requires a reachable embedder backend.
type SemanticIndexConfig struct {
	Enabled  bool   `yaml:"enabled,omitempty"`
	Embedder string `yaml:"embedder,omitempty"` // currently only "ollama"
	Model    string `yaml:"model,omitempty"`    // e.g. "nomic-embed-text"
	BaseURL  string `yaml:"base_url,omitempty"` // optional override for embedder endpoint
	TopK     int    `yaml:"top_k,omitempty"`    // default 20
}

// MemoryConfig controls OpenClaw-style persistent project memory.
// Memory is stored as Markdown files under .orchestrator/memory and indexed
// by SQLite FTS5/BM25. When UseEmbeddings is true an embedder backend
// computes dense vectors for hybrid retrieval.
type MemoryConfig struct {
	Enabled           bool    `yaml:"enabled,omitempty"`             // default true
	TopK              int     `yaml:"top_k,omitempty"`               // default 8
	ChunkTokens       int     `yaml:"chunk_tokens,omitempty"`        // default 400
	OverlapTokens     int     `yaml:"overlap_tokens,omitempty"`      // default 80
	HybridAlpha       float64 `yaml:"hybrid_alpha,omitempty"`        // default 0.5; 1.0 = pure BM25
	MaxRecallChars    int     `yaml:"max_recall_chars,omitempty"`    // default 6000
	MaxPinnedChars    int     `yaml:"max_pinned_chars,omitempty"`    // default 4000
	UseEmbeddings     bool    `yaml:"use_embeddings,omitempty"`      // default false
	Embedder          string  `yaml:"embedder,omitempty"`            // "openai" | "ollama" | "cybertron"
	EmbedderModel     string  `yaml:"embedder_model,omitempty"`      // default per backend
	EmbedderBaseURL   string  `yaml:"embedder_base_url,omitempty"`   // for openai/ollama
	EmbedderAPIKey    string  `yaml:"embedder_api_key,omitempty"`    // env var indirection acceptable
	EmbedderModelsDir string  `yaml:"embedder_models_dir,omitempty"` // cybertron model cache; default ~/.orchestrator/cybertron-models
	AutoPromote       bool    `yaml:"auto_promote,omitempty"`        // default true; promote decisions to MEMORY.md
}

type AgentConfig struct {
	Runner           string   `yaml:"runner,omitempty"`
	Model            string   `yaml:"model,omitempty"`
	MaxContextTokens int      `yaml:"max_context_tokens,omitempty"` // 0 = no limit (for local models)
	Skills           []string `yaml:"-"`                            // always from defaults, never persisted
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

// mergeMemory copies non-zero fields from src into dst. A few fields (Enabled,
// UseEmbeddings, AutoPromote) are booleans, so the convention is "src=true
// overrides", "src=false leaves dst untouched". To explicitly disable, set
// the field at the level where you want it disabled and rely on layered load.
func mergeMemory(dst *MemoryConfig, src MemoryConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.TopK > 0 {
		dst.TopK = src.TopK
	}
	if src.ChunkTokens > 0 {
		dst.ChunkTokens = src.ChunkTokens
	}
	if src.OverlapTokens > 0 {
		dst.OverlapTokens = src.OverlapTokens
	}
	if src.HybridAlpha > 0 {
		dst.HybridAlpha = src.HybridAlpha
	}
	if src.MaxRecallChars > 0 {
		dst.MaxRecallChars = src.MaxRecallChars
	}
	if src.MaxPinnedChars > 0 {
		dst.MaxPinnedChars = src.MaxPinnedChars
	}
	if src.UseEmbeddings {
		dst.UseEmbeddings = true
	}
	if src.Embedder != "" {
		dst.Embedder = src.Embedder
	}
	if src.EmbedderModel != "" {
		dst.EmbedderModel = src.EmbedderModel
	}
	if src.EmbedderBaseURL != "" {
		dst.EmbedderBaseURL = src.EmbedderBaseURL
	}
	if src.EmbedderAPIKey != "" {
		dst.EmbedderAPIKey = src.EmbedderAPIKey
	}
	if src.EmbedderModelsDir != "" {
		dst.EmbedderModelsDir = src.EmbedderModelsDir
	}
	if src.AutoPromote {
		dst.AutoPromote = true
	}
}

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
	if len(src.Project.Context.ExcludePatterns) > 0 {
		dst.Project.Context.ExcludePatterns = src.Project.Context.ExcludePatterns
	}
	if src.Project.Context.SemanticIndex.Enabled {
		dst.Project.Context.SemanticIndex.Enabled = true
	}
	if src.Project.Context.SemanticIndex.Embedder != "" {
		dst.Project.Context.SemanticIndex.Embedder = src.Project.Context.SemanticIndex.Embedder
	}
	if src.Project.Context.SemanticIndex.Model != "" {
		dst.Project.Context.SemanticIndex.Model = src.Project.Context.SemanticIndex.Model
	}
	if src.Project.Context.SemanticIndex.BaseURL != "" {
		dst.Project.Context.SemanticIndex.BaseURL = src.Project.Context.SemanticIndex.BaseURL
	}
	if src.Project.Context.SemanticIndex.TopK > 0 {
		dst.Project.Context.SemanticIndex.TopK = src.Project.Context.SemanticIndex.TopK
	}
	if src.Project.Context.ScopedContextShadow {
		dst.Project.Context.ScopedContextShadow = true
	}
	if src.Project.Context.ScopedContextCoderFix {
		dst.Project.Context.ScopedContextCoderFix = true
	}
	mergeMemory(&dst.Project.Context.Memory, src.Project.Context.Memory)
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
			if ac.MaxContextTokens > 0 {
				existing.MaxContextTokens = ac.MaxContextTokens
			}
			dst.Agents[role] = existing
		}
	}
}
