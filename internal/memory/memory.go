package memory

import (
	"context"
	"fmt"
	"strings"
)

// Recalled bundles everything an agent needs to know about the project from
// past work. Construct via (*Memory).Recall.
type Recalled struct {
	// Pinned is the verbatim content of MEMORY.md (truncated to PinnedCap).
	Pinned string
	// Hits are search results from the rest of the memory corpus.
	Hits []Hit
}

// Memory bundles a Layout and an indexed Store for ergonomic use from the
// rest of the codebase.
type Memory struct {
	Layout Layout
	Store  *Store
}

// Open prepares the memory subsystem for memDir: ensures the layout exists,
// opens the SQLite store at dbPath, and reindexes any file whose hash has
// changed since the last open. Returns a Memory ready for Recall/AppendDaily.
func OpenMemory(ctx context.Context, memDir, dbPath string, opts Options) (*Memory, error) {
	lay, err := NewLayout(memDir)
	if err != nil {
		return nil, err
	}
	st, err := Open(dbPath, lay.Root, opts)
	if err != nil {
		return nil, fmt.Errorf("memory: open store: %w", err)
	}
	m := &Memory{Layout: lay, Store: st}
	if err := m.Reindex(ctx); err != nil {
		// non-fatal: store is still usable, errors are surfaced for logging
		return m, fmt.Errorf("memory: reindex (non-fatal): %w", err)
	}
	return m, nil
}

// Close releases the underlying store.
func (m *Memory) Close() error {
	if m == nil || m.Store == nil {
		return nil
	}
	return m.Store.Close()
}

// Reindex walks every memory file on disk and re-indexes anything stale.
// Also drops index entries for files that have been deleted on disk.
func (m *Memory) Reindex(ctx context.Context) error {
	if m == nil || m.Store == nil {
		return nil
	}
	onDisk := m.Layout.MemoryFiles()
	onDiskSet := make(map[string]struct{}, len(onDisk))
	for _, p := range onDisk {
		onDiskSet[p] = struct{}{}
		if _, err := m.Store.IndexFile(ctx, p); err != nil {
			// keep going — one bad file shouldn't break the whole reindex
			continue
		}
	}
	indexed, err := m.Store.AllFiles(ctx)
	if err != nil {
		return err
	}
	for _, p := range indexed {
		if _, ok := onDiskSet[p]; !ok {
			_ = m.Store.deleteFile(p)
		}
	}
	return nil
}

// Recall builds a context payload for an agent: pinned MEMORY.md plus
// search hits relevant to query. pinnedCap caps the pinned section in chars.
func (m *Memory) Recall(ctx context.Context, query string, opts SearchOptions, pinnedCap int) (Recalled, error) {
	if m == nil {
		return Recalled{}, nil
	}
	r := Recalled{Pinned: capChars(m.Layout.ReadMemory(), pinnedCap)}
	hits, err := m.Store.Search(ctx, query, opts)
	if err != nil {
		return r, err
	}
	r.Hits = hits
	return r, nil
}

// PromptFragment renders Recalled as a Markdown section ready to be appended
// to an agent system prompt. Returns "" if there is nothing to inject.
func (r Recalled) PromptFragment() string {
	if strings.TrimSpace(r.Pinned) == "" && len(r.Hits) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Project Memory\n\n")
	sb.WriteString("Persistent knowledge about this project, captured during previous tasks. ")
	sb.WriteString("Treat these as authoritative unless the current task explicitly overrides them.\n\n")

	if strings.TrimSpace(r.Pinned) != "" {
		sb.WriteString("### Pinned facts (MEMORY.md)\n\n")
		sb.WriteString(strings.TrimSpace(r.Pinned))
		sb.WriteString("\n\n")
	}
	if len(r.Hits) > 0 {
		sb.WriteString("### Recalled from past work\n\n")
		for _, h := range r.Hits {
			fmt.Fprintf(&sb, "**%s** (lines %d-%d, score %.2f)\n\n",
				h.File, h.StartLine, h.EndLine, h.Score)
			sb.WriteString("```\n")
			sb.WriteString(strings.TrimRight(h.Body, "\n"))
			sb.WriteString("\n```\n\n")
		}
	}
	return sb.String()
}

func capChars(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndex(cut, "\n"); i > n/2 {
		cut = cut[:i]
	}
	return cut + "\n... (truncated)"
}
