package index

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/chunker"
)

// fakeEmbedder produces a deterministic 4-dim vector from a sha1 of the text.
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

func TestIndex_AddSearchPersistLoad(t *testing.T) {
	emb := &fakeEmbedder{}
	idx := New(emb)

	chunks := []chunker.Chunk{
		{Kind: "function", Name: "Login", Body: "func Login() { authenticate() }"},
		{Kind: "function", Name: "Logout", Body: "func Logout() {}"},
		{Kind: "function", Name: "Index", Body: "func Index() string { return \"index\" }"},
	}
	paths := []string{"auth.go", "auth.go", "home.go"}

	if err := idx.Add(context.Background(), chunks, paths); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(context.Background(), "func Login() { authenticate() }", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].ChunkName != "Login" {
		t.Errorf("expected Login as best match, got %s", hits[0].ChunkName)
	}

	persistPath := filepath.Join(t.TempDir(), "index.gob")
	if err := idx.Persist(persistPath); err != nil {
		t.Fatal(err)
	}
	idx2 := New(emb)
	if err := idx2.Load(persistPath); err != nil {
		t.Fatal(err)
	}
	hits2, _ := idx2.Search(context.Background(), "func Login() { authenticate() }", 1)
	if len(hits2) != 1 || hits2[0].ChunkName != "Login" {
		t.Errorf("after load, expected Login best, got %+v", hits2)
	}
}

func TestIndex_Remove(t *testing.T) {
	emb := &fakeEmbedder{}
	idx := New(emb)
	chunks := []chunker.Chunk{
		{Name: "A", Body: "a"}, {Name: "B", Body: "b"},
	}
	paths := []string{"x.go", "y.go"}
	if err := idx.Add(context.Background(), chunks, paths); err != nil {
		t.Fatal(err)
	}
	idx.Remove([]string{"x.go"})
	hits, _ := idx.Search(context.Background(), "a", 5)
	for _, h := range hits {
		if h.Path == "x.go" {
			t.Errorf("expected x.go to be removed, found in hits: %+v", hits)
		}
	}
}
