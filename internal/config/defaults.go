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
				Skills: []string{"pm"},
			},
			"coder": {
				Skills: []string{"coder"},
			},
			"coder_fixer": {
				Skills: []string{"coder"},
			},
			"qa": {
				Skills: []string{"qa"},
			},
			"ux_reviewer": {
				Skills: []string{"ux_reviewer"},
			},
			"security": {
				Skills: []string{"security"},
			},
			"pr": {
				Skills: []string{"pr"},
			},
		},
	}
}
