package config

func DefaultConfig() Config {
	return Config{
		PromptLanguage: "English",
		Project: ProjectConfig{
			Name:           "",
			Language:       "go",
			TestCmd:        "go test ./...",
			LintCmd:        "golangci-lint run",
			MaxFixAttempts: 0,
		},
		Agents: map[string]AgentConfig{
			"pm": {
				Skills: []string{"agentic-engineering"},
			},
			"planner": {
				Skills: []string{"agentic-engineering", "architecture-decision-records", "codebase-onboarding", "golang-patterns", "coding-standards"},
			},
			"coder": {
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
			"qa": {
				Skills: []string{"golang-testing", "coding-standards"},
			},
			"pr": {
				Skills: []string{"git-workflow"},
			},
		},
	}
}
