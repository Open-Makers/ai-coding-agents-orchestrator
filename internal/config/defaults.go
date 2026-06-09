package config

func DefaultConfig() Config {
	return Config{
		PromptLanguage: "English",
		Project: ProjectConfig{
			Name:           "",
			Language:       "go",
			TestCmd:        "go test -count=1 ./...",
			LintCmd:        "golangci-lint run",
			MaxFixAttempts: 3,
			Context: ContextConfig{
				Memory: MemoryConfig{
					Enabled:        true,
					TopK:           8,
					ChunkTokens:    400,
					OverlapTokens:  80,
					HybridAlpha:    1.0, // pure BM25 by default (no embedder required)
					MaxRecallChars: 6000,
					MaxPinnedChars: 4000,
					AutoPromote:    true,
				},
			},
		},
		Agents: map[string]AgentConfig{
			"pm": {
				Skills: []string{"project-manager", "agentic-engineering"},
			},
			"coder": {
				Skills: []string{"golang-patterns", "coding-standards", "verification-loop"},
			},
			"coder_fixer": {
				Skills: []string{"golang-patterns", "coding-standards", "verification-loop"},
			},
			"qa": {
				Skills: []string{"golang-testing", "tdd-workflow", "golang-patterns", "coding-standards"},
			},
			"ux_reviewer": {
				Skills: []string{"ux-review", "coding-standards"},
			},
			"security": {
				Skills: []string{"security-scan", "security-review"},
			},
			"pr": {
				Skills: []string{"git-workflow"},
			},
		},
	}
}
