package config

func DefaultConfig() Config {
	return Config{
		Project: ProjectConfig{
			Name:     "",
			Language: "go",
			TestCmd:  "go test ./...",
			LintCmd:  "golangci-lint run",
		},
		Agents: map[string]AgentConfig{
			"planner": {
				Runner: "claude",
				Model:  "claude-sonnet-4-6",
				Skills: []string{"agentic-engineering", "architecture-decision-records"},
			},
			"coder": {
				Runner: "claude",
				Model:  "claude-sonnet-4-6",
				Skills: []string{"golang-patterns"},
			},
			"tester": {
				Runner: "codex",
				Skills: []string{"golang-testing", "tdd-workflow"},
			},
			"reviewer": {
				Runner: "claude",
				Model:  "claude-opus-4-6",
				Skills: []string{"code-review", "security-scan"},
			},
			"fixer": {
				Runner: "claude",
				Model:  "claude-sonnet-4-6",
				Skills: []string{"agent-harness-construction"},
			},
			"pr": {
				Runner: "claude",
				Model:  "claude-sonnet-4-6",
			},
		},
	}
}
