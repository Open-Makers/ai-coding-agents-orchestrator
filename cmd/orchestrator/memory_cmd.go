package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/artifacts"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/config"
	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/memory"
)

// memoryCmd dispatches `orchestrator memory <subcommand>`.
func memoryCmd(args []string) {
	if len(args) == 0 {
		memoryUsage()
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "show":
		memoryShow(rest)
	case "search":
		memorySearch(rest)
	case "reindex":
		memoryReindex(rest)
	case "add":
		memoryAdd(rest)
	case "stats":
		memoryStats(rest)
	case "-h", "--help", "help":
		memoryUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown memory subcommand: %s\n", sub)
		memoryUsage()
		os.Exit(2)
	}
}

func memoryUsage() {
	fmt.Println(`orchestrator memory <subcommand>

Subcommands:
  show              Print MEMORY.md (pinned facts) for the current project
  search "<query>"  Search project memory and print top matches
  reindex           Rebuild the SQLite index from memory/*.md files
  add "<fact>"      Append a fact line to MEMORY.md
  stats             Show counts of indexed files/chunks/embeddings`)
}

func memoryWorkspace() (artifacts.Workspace, config.Config, memory.Layout) {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		fatal(fmt.Errorf("load config: %w", err))
	}
	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(fmt.Errorf("workspace: %w", err))
	}
	lay, err := memory.NewLayout(ws.MemoryDir())
	if err != nil {
		fatal(fmt.Errorf("memory layout: %w", err))
	}
	return ws, cfg, lay
}

func memoryShow(_ []string) {
	_, _, lay := memoryWorkspace()
	content := lay.ReadMemory()
	if strings.TrimSpace(content) == "" {
		fmt.Println("(MEMORY.md is empty — no facts promoted yet)")
		return
	}
	fmt.Print(content)
}

func memorySearch(args []string) {
	fs := flag.NewFlagSet("memory search", flag.ExitOnError)
	k := fs.Int("k", 8, "number of results")
	alpha := fs.Float64("alpha", 1.0, "hybrid weight: 1.0 = pure BM25, 0.5 = balanced")
	_ = fs.Parse(args)
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "memory search: missing query")
		os.Exit(2)
	}

	ws, cfg, _ := memoryWorkspace()
	opts := memory.Options{
		ChunkTokens:   cfg.Project.Context.Memory.ChunkTokens,
		OverlapTokens: cfg.Project.Context.Memory.OverlapTokens,
	}
	ctx := context.Background()
	mem, err := memory.OpenMemory(ctx, ws.MemoryDir(), ws.MemoryDBPath(), opts)
	if err != nil && mem == nil {
		fatal(fmt.Errorf("open memory: %w", err))
	}
	defer func() { _ = mem.Close() }()

	hits, err := mem.Store.Search(ctx, query, memory.SearchOptions{K: *k, HybridAlpha: *alpha})
	if err != nil {
		fatal(fmt.Errorf("search: %w", err))
	}
	if len(hits) == 0 {
		fmt.Println("(no matches)")
		return
	}
	for i, h := range hits {
		fmt.Printf("\n#%d  %s  (score=%.3f bm25=%.3f cos=%.3f)\n", i+1, h.File, h.Score, h.BM25, h.Cosine)
		fmt.Printf("    lines %d-%d\n", h.StartLine, h.EndLine)
		body := strings.TrimSpace(h.Body)
		if len(body) > 500 {
			body = body[:500] + "…"
		}
		for _, ln := range strings.Split(body, "\n") {
			fmt.Println("    " + ln)
		}
	}
}

func memoryReindex(_ []string) {
	ws, cfg, _ := memoryWorkspace()
	opts := memory.Options{
		ChunkTokens:   cfg.Project.Context.Memory.ChunkTokens,
		OverlapTokens: cfg.Project.Context.Memory.OverlapTokens,
	}
	// Delete cache to force a full rebuild.
	_ = os.Remove(ws.MemoryDBPath())
	ctx := context.Background()
	mem, err := memory.OpenMemory(ctx, ws.MemoryDir(), ws.MemoryDBPath(), opts)
	if err != nil && mem == nil {
		fatal(fmt.Errorf("open memory: %w", err))
	}
	defer func() { _ = mem.Close() }()
	st, err := mem.Store.Stats(ctx)
	if err != nil {
		fatal(fmt.Errorf("stats: %w", err))
	}
	fmt.Printf("reindexed: %d file(s), %d chunk(s), %d embedding(s)\n", st.Files, st.Chunks, st.Embeddings)
}

func memoryAdd(args []string) {
	fact := strings.TrimSpace(strings.Join(args, " "))
	if fact == "" {
		fmt.Fprintln(os.Stderr, "memory add: missing fact text")
		os.Exit(2)
	}
	_, _, lay := memoryWorkspace()
	n, err := lay.PromoteFacts(time.Now(), "manual", []memory.Fact{{Source: "manual", Text: fact}})
	if err != nil {
		fatal(fmt.Errorf("add fact: %w", err))
	}
	if n == 0 {
		fmt.Println("(duplicate — fact already in MEMORY.md)")
		return
	}
	fmt.Println("added to MEMORY.md")
}

func memoryStats(_ []string) {
	ws, cfg, _ := memoryWorkspace()
	opts := memory.Options{
		ChunkTokens:   cfg.Project.Context.Memory.ChunkTokens,
		OverlapTokens: cfg.Project.Context.Memory.OverlapTokens,
	}
	ctx := context.Background()
	mem, err := memory.OpenMemory(ctx, ws.MemoryDir(), ws.MemoryDBPath(), opts)
	if err != nil && mem == nil {
		fatal(fmt.Errorf("open memory: %w", err))
	}
	defer func() { _ = mem.Close() }()
	st, err := mem.Store.Stats(ctx)
	if err != nil {
		fatal(fmt.Errorf("stats: %w", err))
	}
	fmt.Printf("files:      %d\n", st.Files)
	fmt.Printf("chunks:     %d\n", st.Chunks)
	fmt.Printf("embeddings: %d\n", st.Embeddings)
}
