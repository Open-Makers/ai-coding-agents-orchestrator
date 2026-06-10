package orchestrator

import (
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/agent"
)

func TestResolvePipeline(t *testing.T) {
	tests := []struct {
		name       string
		spec       agent.TaskSpec
		brownfield bool
		want       string
	}{
		{"explicit rnd", agent.TaskSpec{Pipeline: "rnd"}, false, agent.PipelineRnD},
		{"explicit brown wins over scope", agent.TaskSpec{Pipeline: "brown", Scope: "greenfield"}, false, agent.PipelineBrown},
		{"invalid pipeline falls back to scope", agent.TaskSpec{Pipeline: "nonsense", Scope: "bugfix"}, true, agent.PipelineFix},
		{"bugfix scope -> fix", agent.TaskSpec{Scope: "bugfix"}, true, agent.PipelineFix},
		{"greenfield on empty repo -> green", agent.TaskSpec{Scope: "greenfield"}, false, agent.PipelineGreen},
		{"greenfield on brownfield repo -> brown", agent.TaskSpec{Scope: "greenfield"}, true, agent.PipelineBrown},
		{"feature on brownfield repo -> brown", agent.TaskSpec{Scope: "feature"}, true, agent.PipelineBrown},
		{"feature on empty repo -> green", agent.TaskSpec{Scope: "feature"}, false, agent.PipelineGreen},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvePipeline(tc.spec, tc.brownfield); got != tc.want {
				t.Errorf("resolvePipeline(%+v, %v) = %q, want %q", tc.spec, tc.brownfield, got, tc.want)
			}
		})
	}
}

func TestIsAffirmative(t *testing.T) {
	yes := []string{"yes", "Tak", "ok", "okay", "sure", "accept", "zgoda", "go ahead", "Yes, please"}
	no := []string{"", "no", "nie", "not yet", "maybe", "let's keep going"}
	for _, s := range yes {
		if !isAffirmative(s) {
			t.Errorf("isAffirmative(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isAffirmative(s) {
			t.Errorf("isAffirmative(%q) = true, want false", s)
		}
	}
}
