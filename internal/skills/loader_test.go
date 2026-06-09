package skills

import (
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
)

func TestLoader_LoadEmbeddedSkill(t *testing.T) {
	l := New("")

	content, err := l.Load("coder")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty content for coder")
	}

	// Second load should hit in-memory cache.
	content2, err := l.Load("coder")
	if err != nil {
		t.Fatalf("Load (cache hit) error: %v", err)
	}
	if content2 != content {
		t.Errorf("cache hit returned different content")
	}
}

func TestLoader_DefaultAgentSkillsAllLoad(t *testing.T) {
	l := New("")
	// Every skill referenced by the default per-agent layout must be embedded
	// and loadable, including the consolidated per-agent skills (e.g. pm, coder).
	for _, ac := range config.DefaultConfig().Agents {
		for _, name := range ac.Skills {
			content, err := l.Load(name)
			if err != nil {
				t.Errorf("default skill %q failed to load: %v", name, err)
			}
			if content == "" {
				t.Errorf("default skill %q is empty", name)
			}
		}
	}

	if _, err := l.Load("pm"); err != nil {
		t.Errorf("pm skill must exist: %v", err)
	}
}

func TestLoader_NotFound(t *testing.T) {
	l := New("")

	_, err := l.Load("nonexistent-skill")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestPrefetch(t *testing.T) {
	l := New("")

	err := l.Prefetch([]string{"coder", "qa"})
	if err != nil {
		t.Fatalf("Prefetch error: %v", err)
	}

	content, err := l.Load("coder")
	if err != nil {
		t.Fatalf("Load after Prefetch error: %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty content after Prefetch")
	}
}

func TestPrefetch_WithMissing(t *testing.T) {
	l := New("")

	err := l.Prefetch([]string{"coder", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent skill in Prefetch")
	}
}

func TestAvailable(t *testing.T) {
	l := New("")
	available := l.Available()

	if len(available) == 0 {
		t.Fatal("expected at least one available skill")
	}

	found := false
	for _, name := range available {
		if name == "coder" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("coder not found in available skills: %v", available)
	}
}
