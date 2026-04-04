package orchestrator

import "github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"

type State string

const (
	StateArchitecture State = "ARCHITECTURE"
	StatePlan         State = "PLAN"
	StatePrompts      State = "PROMPTS"
	StateCode         State = "CODE"
	StateTest         State = "TEST"
	StateReview       State = "REVIEW"
	StateFix          State = "FIX"
	StateDone         State = "DONE"
)

type Config struct {
	MaxFixAttempts int
}

type Context struct {
	Root        string
	TaskPath    string
	Workspace   artifacts.Workspace
	State       State
	FixAttempts int
}

type TestResult struct {
	Success bool
	Command string
	Output  string
}

type ReviewResult struct {
	Approve    bool
	MustFix    []string
	NiceToHave []string
}

type FailureContext struct {
	Test   *TestResult
	Review *ReviewResult
}

type Runner interface {
	Architecture(ctx *Context) error
	Plan(ctx *Context) error
	Prompts(ctx *Context) error
	Code(ctx *Context) error
	Review(ctx *Context) error
	Fix(ctx *Context, failure FailureContext) error
	Docs(ctx *Context) error
}

type Tester interface {
	Run(ctx *Context) (TestResult, error)
}

type ReviewParser interface {
	Parse(ctx *Context) (ReviewResult, error)
}
