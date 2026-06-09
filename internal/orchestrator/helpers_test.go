package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/bus"
)

func TestInferBrownfieldScope(t *testing.T) {
	tests := []struct {
		name   string
		inputs []string
		want   string
	}{
		{"english fix", []string{"please fix the login bug"}, "bugfix"},
		{"english bug word", []string{"there is a bug in the parser"}, "bugfix"},
		{"polish poprawka", []string{"trzeba zrobić poprawki w panelu"}, "bugfix"},
		{"polish napraw", []string{"napraw błąd w logowaniu"}, "bugfix"},
		{"english refactor", []string{"refactor the auth module"}, "refactor"},
		{"polish refaktor", []string{"zrób refaktor tego pakietu"}, "refactor"},
		{"neutral feature", []string{"add a new export button"}, "feature"},
		{"empty inputs", []string{"", ""}, "feature"},
		{"description over title", []string{"do something", "fix the crash"}, "bugfix"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inferBrownfieldScope(tc.inputs...)
			if got != tc.want {
				t.Errorf("inferBrownfieldScope(%v) = %q, want %q", tc.inputs, got, tc.want)
			}
		})
	}
}


func TestEmitSummary_IncludesTimeAndStats(t *testing.T) {
	b := bus.New()
	sub := b.Subscribe()

	ws := artifacts.Workspace{Dir: t.TempDir()}
	stats := summaryStats{
		startedAt:     time.Now().Add(-90 * time.Second),
		codingStarted: time.Now().Add(-60 * time.Second),
		agentDurations: map[bus.AgentRole]time.Duration{
			bus.RoleCoder: 45 * time.Second,
		},
		usageByRole: map[bus.AgentRole]bus.AgentUsage{
			bus.RoleCoder: {InputTokens: 12000, OutputTokens: 3400, Estimated: true},
		},
		subTasks:     3,
		fixRounds:    2,
		filesTouched: 7,
		niceToHave:   4,
	}

	emitSummary(b, ws, map[bus.AgentRole]agent.Agent{}, map[string][]string{}, stats)

	// Drain the bus and reassemble the streamed summary text.
	var got string
	deadline := time.After(time.Second)
drain:
	for {
		select {
		case msg := <-sub:
			if tp, ok := msg.Payload.(bus.TokenPayload); ok {
				got += tp.Text
			}
		case <-deadline:
			break drain
		default:
			break drain
		}
	}

	for _, want := range []string{
		"Total time:",
		"Sub-tasks", "3",
		"Files written", "7",
		"Quality rounds", "2",
		"Token Usage:",
		"12.0k", "3.4k",
		"≈ estimated",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q\n---\n%s", want, got)
		}
	}
}
