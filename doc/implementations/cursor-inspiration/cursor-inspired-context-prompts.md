# Implementation Prompts — Cursor-Inspired Context

Stage-by-stage prompts derived from [`cursor-inspired-context-plan.md`](cursor-inspired-context-plan.md). Each prompt is self-contained: it can be pasted into a coding agent (Coder, Claude, etc.) without additional context beyond the repository itself.

Stages are ordered for safe sequential rollout. Within a stage, run prompts top-to-bottom — later prompts assume earlier ones landed.

---

## Stage 1 — Noise Filtering (item 3 of the plan)

### Prompt 1.1 — extend the excluded directory list

> In `internal/context/collector.go`, extend the `excludedTopLevelDirs` slice with the following directories: `dist/`, `build/`, `target/`, `out/`, `.next/`, `.nuxt/`, `.cache/`, `.parcel-cache/`, `__pycache__/`, `.venv/`, `venv/`, `.tox/`, `coverage/`, `.coverage/`, `tmp/`, `.idea/`, `.vscode/`. Keep the existing entries. Do not change any other logic in this file. Add a `collector_test.go` table-driven case verifying that paths under each new directory are excluded by `filterInternalPaths` and by `gitArgsWithExcludes`.

### Prompt 1.2 — add a noise file filter

> In `internal/context/collector.go`, add a new unexported function `isNoiseFile(path string) bool` that returns `true` for: lockfiles (`package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, `Cargo.lock`, `Gemfile.lock`, `poetry.lock`), minified assets (files matching `*.min.js` or `*.min.css`), and source maps (`*.map`). Wire it into `filterInternalPaths` so noise files are filtered out alongside internal-path files. Do NOT include `go.sum` in the default list — it is often needed. Add table-driven tests in `collector_test.go` covering each rule plus a negative case.

### Prompt 1.3 — make extra exclusions configurable

> In `internal/config/config.go`, extend the `Project.Context` config section with a new optional field `ExcludePatterns []string` (YAML key `exclude_patterns`). In `internal/context/collector.go`, after the built-in `isNoiseFile` check, also drop any file matching one of the user-supplied glob patterns (use `path/filepath.Match`). Keep the field optional — when empty, behavior is unchanged. Add a `config_test.go` case loading a YAML with `exclude_patterns` and a `collector_test.go` case showing a custom pattern (e.g. `*.generated.go`) excludes matching files.

---

## Stage 2 — Go Graph Traversal (item 1 of the plan)

### Prompt 2.1 — extract imports and callers

> Create a new file `internal/context/graph.go` with two exported functions:
>
> - `func ImportsOf(root, file string) ([]string, error)` — given a repository root and a file path relative to it, return the repository-relative paths of files imported by it. For `.go` files use `go/parser` from stdlib (`parser.ParseFile` with `parser.ImportsOnly`) and resolve the imports against the module's own packages only (skip stdlib and third-party — they are not in the repo). For other extensions return `nil, nil` for now (a future change will handle them).
> - `func CallersOf(root, symbol string) ([]string, error)` — return the repository-relative paths of files that mention `symbol` as a whole word, excluding `file` itself if obvious. Implement it via `git grep -lw -- <symbol>` using `internal/executil`.
>
> Both functions must respect `excludedTopLevelDirs` from `collector.go` (filter results through `filterInternalPaths`). Add `graph_test.go` with a fixture repo (use `t.TempDir()` + `git init`) covering: a Go file importing another local package, a symbol called from two files, and an empty result case.

### Prompt 2.2 — seed-based ranking in collector

> In `internal/context/collector.go`, change `rankSourceFiles` to accept an optional `seeds []string` parameter (repository-relative paths). For every file `f`:
>
> - if `f` is a seed → add `+200` to its score
> - if `f` appears in `ImportsOf(root, seed)` for any seed → add `+80`
> - if `f` appears in `CallersOf(root, exportedSymbolFrom(seed))` for any seed → add `+60`
>
> Keep the existing name-based scoring as the baseline. Update `collectSourceFiles` to thread the `seeds` slice through. Update `Collect` to accept seeds (default `nil`). Cover the new scoring with a `collector_test.go` case asserting that a seed and its imports rank above unrelated files of equal name-based score.

### Prompt 2.3 — wire seeds into the review context

> In `internal/agent/source_context.go`, change `buildCompactSourceContext` to accept an additional `seeds []string` argument. Before the existing loop, expand `files` by prepending: every seed, then `ImportsOf(root, seed)` for each seed, then `CallersOf(root, primarySymbolOf(seed))` for each seed. De-duplicate while preserving order. Cap the expanded list at the existing token budget. Update every caller (search the repo) to pass the relevant seeds — for the Coder fix path, seeds are the files referenced in the failing test output; elsewhere pass `nil`. Add a `source_context_test.go` case asserting that, given a seed, its imports appear in the rendered output before unrelated files.

---

## Stage 3 — Semantic Chunking (item 2 of the plan)

### Prompt 3.1 — chunker package skeleton with Go support

> Create a new package `internal/chunker/` with:
>
> - `type Chunk struct { Kind string; Name string; StartLine int; EndLine int; Body string }` (Kind is one of `function`, `method`, `type`, `var`, `const`, `file`)
> - `func Chunk(path string, content []byte) ([]Chunk, error)`
>
> For `.go` files implement chunking with `go/parser` + `go/ast`: produce one `Chunk` per top-level declaration (`*ast.FuncDecl`, `*ast.GenDecl`). Preserve the original byte ranges so `Body` is verbatim source. For unsupported extensions return a single `Chunk{Kind: "file", Body: string(content)}`. Add `chunker_test.go` with a Go fixture verifying every chunk parses standalone via `parser.ParseFile` (wrap with a synthetic `package` line if needed) and that line ranges are correct.

### Prompt 3.2 — budget-aware truncation in source_context

> In `internal/agent/source_context.go`, replace the current `fileContent[:maxReviewFileSize]` truncation. New behavior: call `chunker.Chunk(path, content)` on each file. If the file fits within the per-file budget (`maxReviewFileSize`), include it whole. Otherwise include whole chunks until the budget is exhausted, ordered: exported `func`/`type` first, then unexported, then `const`/`var`. Never split a chunk. If the very first chunk exceeds the budget, include it and stop. Add a test asserting no chunk in the rendered output ends with an unbalanced brace count.

### Prompt 3.3 — apply the same chunking to brownfield context

> In `internal/context/collector.go`, update `collectSourceFiles` to use `chunker.Chunk` the same way as Stage 3.2. The per-file budget is `maxSourceFileSize`; the global budget is `maxTotalSourceContext`. Order chunks within a file the same way (exported first). Update `SystemPromptFragment`'s `### Existing Source Code` rendering accordingly: if a file was chunked, label the truncation as `... (chunks omitted)` instead of `... (truncated)`. Update existing tests; add one case asserting a large file is included as whole functions, never mid-function.

### Prompt 3.4 — optional: tree-sitter for TS/JS/Python/Rust

> Extend `internal/chunker/` with tree-sitter support for `.ts`, `.tsx`, `.js`, `.jsx`, `.py`, `.rs`. Use `github.com/smacker/go-tree-sitter` and the per-language grammar packages. Map grammar node types to `Chunk.Kind`: `function_declaration`/`method_definition` → `function`/`method`, `class_declaration` → `type`, etc. Keep the Go path on stdlib (do not route it through tree-sitter). Add a build tag `treesitter` so the dependency is opt-in; without the tag, non-Go files fall back to whole-file chunks. Add tests under the build tag for one fixture per language.

---

## Stage 4 — Semantic Index (item 4 of the plan)

### Prompt 4.1 — embedder interface and Ollama implementation

> Create a new package `internal/embedder/`:
>
> - `type Embedder interface { Embed(ctx context.Context, texts []string) ([][]float32, error); Dim() int }`
> - implementation `type ollamaEmbedder struct { ... }` calling Ollama's `/api/embeddings` HTTP endpoint with a configurable model (default `nomic-embed-text`). Reuse `internal/runner/ollama.go` HTTP client patterns where applicable. Handle errors with typed errors: `ErrEmbedderUnavailable`, `ErrEmbedderRequest`.
> - factory `func New(cfg config.EmbedderConfig) (Embedder, error)`
>
> Add `embedder_test.go` using `httptest.NewServer` to mock Ollama responses; cover success, server error, and unreachable cases.

### Prompt 4.2 — index package on top of chromem-go

> Add `github.com/philippgille/chromem-go` to `go.mod`. Create a new package `internal/index/`:
>
> - `type Index interface { Add(ctx context.Context, chunks []chunker.Chunk, paths []string) error; Search(ctx context.Context, query string, k int) ([]Hit, error); Persist(path string) error; Load(path string) error }`
> - `type Hit struct { Path string; ChunkName string; Score float32 }`
> - implementation backed by `chromem.NewDB()` with one collection named `code`; persist to `.orchestrator/index/chromem.gob` via the library's serialization
> - constructor `func New(emb embedder.Embedder) Index`
>
> Add `index_test.go` with a fake embedder (returns deterministic vectors keyed by text hash) verifying: chunks added → query returns nearest by cosine, persist+load round-trip preserves results.

### Prompt 4.3 — wire the index into Collect

> Extend the config in `internal/config/config.go` with a `SemanticIndex` section under `Project.Context`:
>
> ```yaml
> context:
>   semantic_index:
>     enabled: false
>     embedder: ollama
>     model: nomic-embed-text
>     top_k: 20
> ```
>
> In `internal/context/collector.go`, when `cfg.Project.Context.SemanticIndex.Enabled` is true: after `git ls-files`, build/refresh the index by chunking every source file (`internal/chunker`) and feeding chunks into `internal/index`. Persist to `.orchestrator/index/`. Add a method `func (p ProjectContext) SemanticSearch(ctx context.Context, query string, k int) ([]string, error)` that returns deduplicated file paths for the top-K hits. When the flag is off, the method returns `nil, nil`. Cover with a `collector_test.go` case using a fake embedder.

### Prompt 4.4 — use semantic search as the seed source

> In `internal/agent/source_context.go`, when seeds are empty AND `ProjectContext.SemanticSearch` is available, derive seeds from `SemanticSearch(stagePrompt, cfg.Project.Context.SemanticIndex.TopK)` before expanding the graph (Stage 2.3). Plumb `stagePrompt` through every relevant caller — the prompt is whatever instruction the agent is about to receive. Keep the existing behavior when the index is disabled. Add a test with a fake embedder asserting that a query about "authentication" surfaces a file containing only `login.go`-style code, even when its name is unrelated.

---

## Stage 5 — Merkle Cache (item 5 of the plan)

### Prompt 5.1 — fingerprints and diff

> Extend `internal/index/` with:
>
> - `type FileFingerprint struct { Path string; SHA256 string }`
> - `func BuildFingerprints(root string, files []string) (map[string]string, error)` — SHA-256 per file, path-relative key
> - `func DiffFingerprints(prev, curr map[string]string) (added, modified, deleted []string)`
> - persistence: `func SaveFingerprints(path string, fp map[string]string) error` / `LoadFingerprints` to `.orchestrator/index/fingerprints.json`
>
> Use `crypto/sha256` from stdlib. Cover with a `fingerprints_test.go` case: identical inputs → empty diff; one file modified → only that path in `modified`; deleted file → only that path in `deleted`.

### Prompt 5.2 — incremental indexing

> Modify the indexing path in `internal/context/collector.go` (Stage 4.3): on each run, load the previous fingerprint snapshot. Compute the diff against the current `git ls-files`. Re-chunk and re-embed only `added + modified`; remove embeddings for `deleted` (add `Index.Remove(paths []string)` if missing). Save the new snapshot after indexing succeeds. If snapshot loading fails, fall back to full reindex. Add a `collector_test.go` case asserting that a second `Collect()` on an unchanged repo invokes the fake embedder zero times, and modifying one file invokes it exactly once.

---

## Notes on Use

- Each prompt assumes the project's coding rules in `AGENTS.md`/`CLAUDE.md` (surgical changes, simplicity first, tests before claiming done).
- Prompts within Stages 1, 2, 3 can be merged into a single PR per stage; Stages 4 and 5 are large enough to deserve a PR per prompt.
- Before starting Stage 4, verify with the maintainer whether the optional Ollama dependency is acceptable as a runtime requirement for the feature flag.

