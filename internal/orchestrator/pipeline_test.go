package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

// stubAgent is a minimal Agent for testing.
type stubAgent struct {
	role    bus.AgentRole
	runFn   func(ctx context.Context, msg bus.Message) (bus.Message, error)
}

func (s *stubAgent) Role() bus.AgentRole { return s.role }
func (s *stubAgent) Run(ctx context.Context, msg bus.Message) (bus.Message, error) {
	return s.runFn(ctx, msg)
}

// TestTestAndReview_Parallel verifies that Tester and Reviewer run concurrently.
func TestTestAndReview_Parallel(t *testing.T) {
	b := bus.New()

	// Track the maximum concurrent overlap between tester and reviewer.
	var running int32
	var maxConcurrent int32

	makeAgent := func(role bus.AgentRole, payload any) *stubAgent {
		return &stubAgent{
			role: role,
			runFn: func(ctx context.Context, msg bus.Message) (bus.Message, error) {
				cur := atomic.AddInt32(&running, 1)
				for {
					peak := atomic.LoadInt32(&maxConcurrent)
					if cur <= peak || atomic.CompareAndSwapInt32(&maxConcurrent, peak, cur) {
						break
					}
				}
				// Simulate work.
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&running, -1)
				return bus.NewMessage(role, "", bus.MsgResponse, payload), nil
			},
		}
	}

	agents := map[bus.AgentRole]agent.Agent{
		bus.RoleTester:   makeAgent(bus.RoleTester, agent.TestReport{Success: true}),
		bus.RoleReviewer: makeAgent(bus.RoleReviewer, agent.ReviewResult{Approved: true}),
	}

	p := &Pipeline{b: b, agents: agents}

	ctx := context.Background()
	testOK, reviewOK, _, err := p.testAndReview(ctx)
	if err != nil {
		t.Fatalf("testAndReview: %v", err)
	}
	if !testOK {
		t.Error("expected testOK=true")
	}
	if !reviewOK {
		t.Error("expected reviewOK=true")
	}

	// Both agents must have been running at the same time.
	if atomic.LoadInt32(&maxConcurrent) < 2 {
		t.Error("expected tester and reviewer to run concurrently (maxConcurrent < 2)")
	}
}
