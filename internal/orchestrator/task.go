package orchestrator

// TaskScope describes the magnitude of a task, influencing the execution strategy.
// Kept here as convenience aliases — canonical types are in the agent package.
type TaskScope = string

const (
	ScopeGreenfield TaskScope = "greenfield"
	ScopeFeature    TaskScope = "feature"
	ScopeBugfix     TaskScope = "bugfix"
	ScopeRefactor   TaskScope = "refactor"
)
