// Package index stores embeddings for source-code chunks and answers
// nearest-neighbor queries via cosine similarity. The implementation is
// intentionally minimal (in-memory + gob on disk) to avoid pulling in a
// vector-DB dependency for what is currently an opt-in feature.
package index

import (
	"context"
	"encoding/gob"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/chunker"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/embedder"
)

// Hit is a search result (file, chunk name, similarity score in [-1, 1]).
type Hit struct {
	Path      string
	ChunkName string
	Score     float32
}

// Index stores chunk embeddings and answers similarity queries.
type Index interface {
	Add(ctx context.Context, chunks []chunker.Chunk, paths []string) error
	Remove(paths []string)
	Search(ctx context.Context, query string, k int) ([]Hit, error)
	Persist(path string) error
	Load(path string) error
}

// New constructs an in-memory index backed by emb for embeddings.
func New(emb embedder.Embedder) Index {
	return &memIndex{emb: emb}
}

type entry struct {
	Path      string
	ChunkName string
	Vector    []float32
}

type memIndex struct {
	mu      sync.RWMutex
	entries []entry
	emb     embedder.Embedder
}

func (m *memIndex) Add(ctx context.Context, chunks []chunker.Chunk, paths []string) error {
	if len(chunks) != len(paths) {
		return errors.New("index.Add: chunks and paths length mismatch")
	}
	if len(chunks) == 0 {
		return nil
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Body
	}
	vecs, err := m.emb.Embed(ctx, texts)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, v := range vecs {
		m.entries = append(m.entries, entry{Path: paths[i], ChunkName: chunks[i].Name, Vector: v})
	}
	return nil
}

func (m *memIndex) Remove(paths []string) {
	if len(paths) == 0 {
		return
	}
	drop := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		drop[p] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.entries[:0]
	for _, e := range m.entries {
		if _, gone := drop[e.Path]; gone {
			continue
		}
		kept = append(kept, e)
	}
	m.entries = kept
}

func (m *memIndex) Search(ctx context.Context, query string, k int) ([]Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	vecs, err := m.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	q := vecs[0]
	m.mu.RLock()
	defer m.mu.RUnlock()
	hits := make([]Hit, 0, len(m.entries))
	for _, e := range m.entries {
		hits = append(hits, Hit{Path: e.Path, ChunkName: e.ChunkName, Score: cosine(q, e.Vector)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

func (m *memIndex) Persist(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.Create(path) // #nosec G304 -- path is constructed from project root + constant
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return gob.NewEncoder(f).Encode(m.entries)
}

func (m *memIndex) Load(path string) error {
	f, err := os.Open(path) // #nosec G304 -- path is constructed from project root + constant
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var loaded []entry
	if err := gob.NewDecoder(f).Decode(&loaded); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = loaded
	return nil
}

func cosine(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
