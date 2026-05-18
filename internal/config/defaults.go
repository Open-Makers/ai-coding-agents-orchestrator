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
		},
		Agents: map[string]AgentConfig{
			"pm": {
				Skills: []string{"agentic-engineering"},
			},
			"architect": {
				Skills: []string{"agentic-engineering", "architecture-decision-records", "codebase-onboarding"},
			},
			"planner": {
				Skills: []string{"agentic-engineering", "architecture-decision-records", "codebase-onboarding", "golang-patterns", "coding-standards"},
			},
			"coder": {
				Skills: []string{"golang-patterns", "coding-standards", "verification-loop"},
			},
			"coder_fixer": {
				Skills: []string{"golang-patterns", "coding-standards", "verification-loop"},
			},
			"tester": {
				Skills: []string{"golang-testing", "tdd-workflow"},
			},
			"reviewer": {
				Skills: []string{"golang-patterns", "coding-standards"},
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
