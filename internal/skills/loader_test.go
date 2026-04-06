package skills

import (
	"testing"
)

func TestLoader_LoadEmbeddedSkill(t *testing.T) {
	l := New("")

	content, err := l.Load("golang-patterns")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty content for golang-patterns")
	}

	// Second load should hit in-memory cache.
	content2, err := l.Load("golang-patterns")
	if err != nil {
		t.Fatalf("Load (cache hit) error: %v", err)
	}
	if content2 != content {
		t.Errorf("cache hit returned different content")
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

	err := l.Prefetch([]string{"golang-patterns", "golang-testing"})
	if err != nil {
		t.Fatalf("Prefetch error: %v", err)
	}

	content, err := l.Load("golang-patterns")
	if err != nil {
		t.Fatalf("Load after Prefetch error: %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty content after Prefetch")
	}
}

func TestPrefetch_WithMissing(t *testing.T) {
	l := New("")

	err := l.Prefetch([]string{"golang-patterns", "nonexistent"})
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
		if name == "golang-patterns" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("golang-patterns not found in available skills: %v", available)
	}
}
