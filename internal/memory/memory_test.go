package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChunkMarkdown_SplitsByHeadings(t *testing.T) {
	doc := strings.Repeat("alpha beta gamma\n", 20) +
		"\n## Section Two\n" +
		strings.Repeat("delta epsilon zeta\n", 20) +
		"\n## Section Three\n" +
		strings.Repeat("eta theta iota\n", 20)

	chunks := ChunkMarkdown(doc, 100, 20)
	if len(chunks) < 2 {
		t.Fatalf("want >=2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Body == "" {
			t.Errorf("chunk %d empty", i)
		}
	}
}

func TestChunkMarkdown_Empty(t *testing.T) {
	if got := ChunkMarkdown("", 400, 80); got != nil {
		t.Errorf("want nil, got %v", got)
	}
	if got := ChunkMarkdown("   \n   \n", 400, 80); got != nil {
		t.Errorf("want nil on whitespace, got %v", got)
	}
}

func TestStore_IndexAndSearchBM25(t *testing.T) {
	dir := t.TempDir()
	lay, err := NewLayout(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lay.AppendDaily(time.Now(), "coder",
		"Implemented JWT authentication with bcrypt password hashing in internal/auth"); err != nil {
		t.Fatal(err)
	}
	if err := lay.AppendDaily(time.Now().Add(-24*time.Hour), "architect",
		"Decided to use PostgreSQL for the primary database. Redis for caching."); err != nil {
		t.Fatal(err)
	}

	st, err := Open(filepath.Join(dir, "memory.db"), lay.Root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	for _, f := range lay.MemoryFiles() {
		if _, err := st.IndexFile(context.Background(), f); err != nil {
			t.Fatalf("index %s: %v", f, err)
		}
	}

	hits, err := st.Search(context.Background(), "PostgreSQL database",
		SearchOptions{K: 3, HybridAlpha: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected at least one hit for PostgreSQL")
	}
	if !strings.Contains(hits[0].Body, "PostgreSQL") {
		t.Errorf("top hit should mention PostgreSQL, got: %s", hits[0].Body)
	}

	hits2, err := st.Search(context.Background(), "JWT authentication",
		SearchOptions{K: 3, HybridAlpha: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits2) == 0 || !strings.Contains(hits2[0].Body, "JWT") {
		t.Errorf("expected JWT hit, got %v", hits2)
	}
}

func TestPromoteFacts_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	lay, err := NewLayout(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	facts := []Fact{
		{Source: "architecture.md#Decisions", Text: "Use PostgreSQL for the database"},
		{Source: "architecture.md#Decisions", Text: "All errors wrap with fmt.Errorf"},
	}
	n, err := lay.PromoteFacts(now, "task-1", facts)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want 2 promoted, got %d", n)
	}
	// Second call with same facts → 0 promoted.
	n2, err := lay.PromoteFacts(now, "task-2", facts)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("want 0 promoted on dedup, got %d", n2)
	}
}

func TestExtractDecisions(t *testing.T) {
	doc := `# Architecture

Some intro.

## Key Decisions

- Use PostgreSQL for persistence
- Avoid CGO dependencies

## Implementation Notes

- This should not be picked up

## Constraints

- Stay within Go 1.26
`
	got := ExtractDecisions(doc, nil)
	want := []string{
		"Use PostgreSQL for persistence",
		"Avoid CGO dependencies",
		"Stay within Go 1.26",
	}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("want %q got %q", want[i], got[i])
		}
	}
}

func TestMemory_RecallAndPromptFragment(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	lay, err := NewLayout(memDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = lay.AppendDaily(time.Now(), "architect",
		"Selected the Bubble Tea framework for the TUI layer.")
	_, err = lay.PromoteFacts(time.Now(), "t1", []Fact{
		{Source: "vision.md", Text: "Project targets terminal-first UX."},
	})
	if err != nil {
		t.Fatal(err)
	}

	mem, err := OpenMemory(context.Background(), memDir,
		filepath.Join(dir, "memory.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()

	r, err := mem.Recall(context.Background(), "Bubble Tea TUI", SearchOptions{K: 3, HybridAlpha: 1.0}, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Pinned, "terminal-first") {
		t.Errorf("pinned should contain promoted fact, got: %s", r.Pinned)
	}
	if len(r.Hits) == 0 {
		t.Fatalf("expected hits for Bubble Tea")
	}
	frag := r.PromptFragment()
	if !strings.Contains(frag, "Project Memory") {
		t.Errorf("fragment missing Project Memory header")
	}
	if !strings.Contains(frag, "Bubble Tea") {
		t.Errorf("fragment missing recalled body: %s", frag)
	}
}
