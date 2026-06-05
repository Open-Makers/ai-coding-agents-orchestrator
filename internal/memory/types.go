package memory

import "fmt"

// FileRef identifies a memory file by workspace-relative path.
type FileRef struct {
	// Path is the workspace-relative path to the memory file
	// (e.g. "MEMORY.md", "daily/2026-05-19.md").
	Path string
}

// Hit is a single search result.
type Hit struct {
	File      string  // workspace-relative path
	StartLine int
	EndLine   int
	Score     float64 // hybrid score (higher = better)
	BM25      float64
	Cosine    float64
	Body      string
}

// String formats a hit for debug output.
func (h Hit) String() string {
	return fmt.Sprintf("%s:%d-%d (score=%.3f)", h.File, h.StartLine, h.EndLine, h.Score)
}

// SearchOptions tunes a search query.
type SearchOptions struct {
	K            int     // top results to return (default 8)
	HybridAlpha  float64 // weight of BM25 vs cosine; 1.0 = pure BM25 (default 0.5)
	MaxChars     int     // total body chars across results (0 = unlimited)
	RerankCandidates int // how many BM25 candidates to rerank by cosine (default 4*K)
}

func (o SearchOptions) withDefaults() SearchOptions {
	if o.K <= 0 {
		o.K = 8
	}
	if o.HybridAlpha < 0 || o.HybridAlpha > 1 {
		o.HybridAlpha = 0.5
	}
	if o.RerankCandidates <= 0 {
		o.RerankCandidates = 4 * o.K
	}
	return o
}
