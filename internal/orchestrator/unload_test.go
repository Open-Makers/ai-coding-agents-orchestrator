package orchestrator

import (
	"context"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestShouldUnloadPrev(t *testing.T) {
	cases := []struct {
		name       string
		prev, next config.AgentConfig
		want       bool
	}{
		{
			name: "local model changed",
			prev: config.AgentConfig{Runner: "ollama", Model: "a"},
			next: config.AgentConfig{Runner: "ollama", Model: "b"},
			want: true,
		},
		{
			name: "local runner changed",
			prev: config.AgentConfig{Runner: "ollama", Model: "a"},
			next: config.AgentConfig{Runner: "lmstudio", Model: "a"},
			want: true,
		},
		{
			name: "local to cloud frees RAM",
			prev: config.AgentConfig{Runner: "ollama", Model: "a"},
			next: config.AgentConfig{Runner: "claude", Model: "sonnet"},
			want: true,
		},
		{
			name: "same local model stays loaded",
			prev: config.AgentConfig{Runner: "ollama", Model: "a"},
			next: config.AgentConfig{Runner: "ollama", Model: "a"},
			want: false,
		},
		{
			name: "cloud prev never unloads",
			prev: config.AgentConfig{Runner: "claude", Model: "sonnet"},
			next: config.AgentConfig{Runner: "ollama", Model: "a"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldUnloadPrev(tc.prev, tc.next); got != tc.want {
				t.Errorf("shouldUnloadPrev(%+v, %+v) = %v, want %v", tc.prev, tc.next, got, tc.want)
			}
		})
	}
}

func TestUnloadOnSwitch_TracksLoaded(t *testing.T) {
	tr := &TaskRunner{
		cfg: config.Config{Agents: map[string]config.AgentConfig{
			"pm":    {Runner: "claude", Model: "sonnet"},
			"coder": {Runner: "claude", Model: "opus"},
		}},
	}
	// No bus/log needed: cloud configs never trigger an unload or publish.
	tr.b = nil

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unloadOnSwitch panicked on cloud configs: %v", r)
		}
	}()

	tr.unloadOnSwitch(context.Background(), "pm")
	if !tr.loadedSet || tr.loadedModel.Model != "sonnet" {
		t.Fatalf("after pm: loadedSet=%v model=%q", tr.loadedSet, tr.loadedModel.Model)
	}
	tr.unloadOnSwitch(context.Background(), "coder")
	if tr.loadedModel.Model != "opus" {
		t.Errorf("after coder: want opus, got %q", tr.loadedModel.Model)
	}
}
