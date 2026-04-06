package orchestrator

import (
	"context"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

// stubAgent is a minimal Agent for testing.
type stubAgent struct {
	role  bus.AgentRole
	runFn func(ctx context.Context, msg bus.Message) (bus.Message, error)
}

func (s *stubAgent) Role() bus.AgentRole { return s.role }
func (s *stubAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	return s.runFn(ctx, msg)
}

// TestGenerateCode_Success verifies the happy path for initial code generation.
func TestGenerateCode_Success(t *testing.T) {
	b := bus.New()

	agents := map[bus.AgentRole]agent.Agent{
		bus.RoleCoder: &stubAgent{
			role: bus.RoleCoder,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, agent.CoderResult{
					Files: []string{"main.go"},
				}), nil
			},
		},
	}

	p := &Pipeline{b: b, agents: agents, niceToHave: make(map[string][]string)}
	files, err := p.generateCode(context.Background(), "plan", "ctx", "", 0, 0, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected files, got none")
	}
}

// TestQualityGate_PassOnFirstTry verifies all quality checks pass without fixes.
func TestQualityGate_PassOnFirstTry(t *testing.T) {
	b := bus.New()

	agents := map[bus.AgentRole]agent.Agent{
		bus.RoleCoder: &stubAgent{
			role: bus.RoleCoder,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, agent.CoderResult{}), nil
			},
		},
		bus.RoleTester: &stubAgent{
			role: bus.RoleTester,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				return bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, agent.TestReport{Success: true}), nil
			},
		},
		bus.RoleReviewer: &stubAgent{
			role: bus.RoleReviewer,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				return bus.NewMessage(bus.RoleReviewer, "", bus.MsgResponse, agent.ReviewResult{Approved: true}), nil
			},
		},
	}

	p := &Pipeline{b: b, agents: agents, niceToHave: make(map[string][]string)}
	files := []string{"main.go"}
	if err := p.qualityGate(context.Background(), "ctx", &files); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

// TestQualityGate_FixAndRestart verifies that a review failure triggers a fix
// and restarts the full quality gate (test → review again).
func TestQualityGate_FixAndRestart(t *testing.T) {
	b := bus.New()

	reviewCalls := 0
	testerCalls := 0
	coderCalls := 0

	agents := map[bus.AgentRole]agent.Agent{
		bus.RoleCoder: &stubAgent{
			role: bus.RoleCoder,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				coderCalls++
				return bus.NewMessage(bus.RoleCoder, "", bus.MsgResponse, agent.CoderResult{}), nil
			},
		},
		bus.RoleTester: &stubAgent{
			role: bus.RoleTester,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				testerCalls++
				return bus.NewMessage(bus.RoleTester, "", bus.MsgResponse, agent.TestReport{Success: true}), nil
			},
		},
		bus.RoleReviewer: &stubAgent{
			role: bus.RoleReviewer,
			runFn: func(_ context.Context, _ bus.Message) (bus.Message, error) {
				reviewCalls++
				if reviewCalls == 1 {
					return bus.NewMessage(bus.RoleReviewer, "", bus.MsgResponse, agent.ReviewResult{
						MustFix: []string{"fix this"},
					}), nil
				}
				return bus.NewMessage(bus.RoleReviewer, "", bus.MsgResponse, agent.ReviewResult{Approved: true}), nil
			},
		},
	}

	cfg := config.Config{}
	p := &Pipeline{b: b, agents: agents, cfg: cfg, niceToHave: make(map[string][]string)}
	files := []string{"main.go"}
	if err := p.qualityGate(context.Background(), "ctx", &files); err != nil {
		t.Fatalf("expected success after fix, got: %v", err)
	}

	// 1 fix call from review failure.
	if coderCalls != 1 {
		t.Errorf("expected 1 coder fix call, got %d", coderCalls)
	}
	// 2 test runs: first pass + after fix restart.
	if testerCalls != 2 {
		t.Errorf("expected 2 tester calls, got %d", testerCalls)
	}
	// 2 review runs: first found issue, second passed.
	if reviewCalls != 2 {
		t.Errorf("expected 2 reviewer calls, got %d", reviewCalls)
	}
}

func TestCleanGeneratedArtifacts_RemovesButKeepsRequirements(t *testing.T) {
	dir := t.TempDir()
	ws, err := artifacts.EnsureWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Write requirements and some generated artifacts.
	_ = ws.WriteFile(artifacts.RequirementsFile, []byte("reqs"))
	_ = ws.WriteFile(artifacts.VisionFile, []byte("vision"))
	_ = ws.WriteFile(artifacts.MoscowFile, []byte("moscow"))
	_ = ws.WriteFile(artifacts.ArchitectureFile, []byte("arch"))
	_ = ws.WriteFile(artifacts.ImplementationPlanFile, []byte("plan"))

	ws.CleanGeneratedArtifacts()

	// Requirements should survive.
	if !ws.FileExists(artifacts.RequirementsFile) {
		t.Error("requirements should not be cleaned")
	}

	// Generated artifacts should be gone.
	for _, name := range []string{
		artifacts.VisionFile,
		artifacts.MoscowFile,
		artifacts.ArchitectureFile,
		artifacts.ImplementationPlanFile,
	} {
		if ws.FileExists(name) {
			t.Errorf("%s should have been cleaned", name)
		}
	}
}
