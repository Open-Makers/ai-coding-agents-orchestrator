package context

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/embedder"
)

// fakeEmbedder produces deterministic 4-dim vectors and counts invocations.
type fakeEmbedder struct{ calls int }

func (f *fakeEmbedder) Dim() int { return 4 }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		f.calls++
		sum := sha1.Sum([]byte(t))
		v := make([]float32, 4)
		for j := 0; j < 4; j++ {
			u := binary.BigEndian.Uint32(sum[j*4 : j*4+4])
			v[j] = float32(int32(u)) / 1e9
		}
		out[i] = v
	}
	return out, nil
}

func setupSemanticRepo(t *testing.T, fake *fakeEmbedder) (string, config.Config) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t.com"}, {"config", "user.name", "t"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module example.com/s\n\ngo 1.22\n")
	mustWrite("auth/login.go", "package auth\n\nfunc Authenticate(user, pass string) bool { return true }\n")
	mustWrite("home/home.go", "package home\n\nfunc Render() string { return \"home\" }\n")
	addAll := exec.Command("git", "add", "-A")
	addAll.Dir = dir
	if out, err := addAll.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	prev := embedderFactory
	t.Cleanup(func() { embedderFactory = prev })
	embedderFactory = func(_ config.SemanticIndexConfig) (embedder.Embedder, error) {
		return fake, nil
	}

	cfg := config.Config{}
	cfg.Project.Context.SemanticIndex = config.SemanticIndexConfig{
		Enabled: true, Embedder: "ollama", Model: "x", TopK: 5,
	}
	return dir, cfg
}

func TestCollect_SemanticIndexBuildAndSearch(t *testing.T) {
	fake := &fakeEmbedder{}
	dir, cfg := setupSemanticRepo(t, fake)

	pc, err := Collect(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if pc.idx == nil {
		t.Fatal("expected idx to be populated when semantic_index is enabled")
	}
	hits, err := pc.SemanticSearch(context.Background(), "func Authenticate(user, pass string) bool { return true }", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0] != "auth/login.go" {
		t.Errorf("expected auth/login.go as top hit, got %v", hits)
	}
}

func TestCollect_SemanticIndexIncremental(t *testing.T) {
	fake := &fakeEmbedder{}
	dir, cfg := setupSemanticRepo(t, fake)

	if _, err := Collect(dir, cfg); err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := fake.calls
	if callsAfterFirst == 0 {
		t.Fatal("expected first run to invoke embedder")
	}

	// Second Collect on unchanged repo → embedder is NOT called (no chunks
	// to re-embed, and Search is not invoked here).
	if _, err := Collect(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if fake.calls != callsAfterFirst {
		t.Errorf("expected zero new embedder calls on unchanged repo, got %d new", fake.calls-callsAfterFirst)
	}

	// Modify one file → embedder is called once for its chunk.
	if err := os.WriteFile(filepath.Join(dir, "auth/login.go"),
		[]byte("package auth\n\nfunc Authenticate(user, pass string) bool { return false }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := fake.calls
	if _, err := Collect(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if fake.calls-prev != 1 {
		t.Errorf("expected exactly 1 embedder call after modification, got %d", fake.calls-prev)
	}
}

func TestCollect_SemanticSearchDisabled(t *testing.T) {
	dir := initGitRepo(t)
	pc, err := Collect(dir, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := pc.SemanticSearch(context.Background(), "anything", 5)
	if err != nil || hits != nil {
		t.Errorf("expected nil hits with index disabled, got %v %v", hits, err)
	}
}
