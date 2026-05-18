# Implementation Plan — Cursor-Inspired Context

This plan covers 5 items extracted from analysis of Cursor's pipeline ([`cursor-jak-dziala.md`](cursor-how-works.md)) against the current context implementation in [`internal/context/collector.go`](../../../internal/context/collector.go) and [`internal/agent/source_context.go`](../../../internal/agent/source_context.go).

Items are ordered by ROI. Each item is independent — they can be rolled out sequentially.

---

## 1. Graph Traversal for Go (imports + callers)

**Problem:** `rankSourceFiles` in [`collector.go`](../../../internal/context/collector.go) picks the top-N files using a name-based heuristic. During the fix phase, the Coder receives the failing file in isolation — without the files it imports and without the files that call it.

**Solution:** after picking a candidate file (e.g. a file flagged by the Tester as failing), pull in:
- files **imported** by the candidate
- files that **call** the symbols exported by the candidate

**Scope of changes:**
- new file `internal/context/graph.go`
  - `func ImportsOf(root, file string) []string` — for `.go` via `go/parser` (stdlib), for other languages a regex over `import`/`from`/`require` lines
  - `func CallersOf(root, symbol string) []string` — `git grep -l <symbol>`
- modify `rankSourceFiles` in `collector.go` — optional "seed files" parameter (e.g. files from errors); their imports/callers get a score boost
- modify `buildCompactSourceContext` in `source_context.go` — accepts a seed file list and expands it with graph neighbors

**Success:**
- a `coder_fix_test.go` scenario: a failing test points at file A → the Coder context contains A + its imports + a file that calls A
- measurable: average number of quality gate iterations in `pipeline_test.go` does not grow (and ideologically should drop)

**External dependencies:** none at runtime; `go/parser` from stdlib.

**Estimated size:** ~150 production LOC + tests.

---

## 2. Semantic Chunking (tree-sitter)

**Problem:** [`source_context.go:51`](../../../internal/agent/source_context.go) and [`collector.go:182`](../../../internal/context/collector.go) truncate files via `content[:maxSourceFileSize]` — a function gets cut in the middle, braces unbalanced, imports dropped.

**Solution:** chunk files by syntactic units (function, class, block). When a file fits within the budget — include it whole. When it doesn't — include whole functions/classes until the budget is exhausted, with a preference for exported ones.

**Scope of changes:**
- new package `internal/chunker/`
  - `func Chunk(path string, content []byte) []Chunk` — `Chunk{Kind, Name, StartLine, EndLine, Body}`
  - per-language implementation:
    - Go → `go/parser` (stdlib, no dependencies)
    - TS/JS/Python/Rust → `github.com/smacker/go-tree-sitter` with the relevant grammars
    - fallback for unsupported languages → whole file as a single chunk
- `source_context.go:51` — instead of `fileContent[:maxReviewFileSize]` pick chunks until the budget is exhausted
- `collector.go:182` — same for brownfield context

**Success:**
- new `chunker_test.go` — table-driven, verifies that chunks are syntactically valid (parseable in isolation)
- no truncated chunk in the output of `buildCompactSourceContext` ends in the middle of a function

**External dependencies:** `github.com/smacker/go-tree-sitter` (CGO — watch out for cross-compile; consider `github.com/tree-sitter/go-tree-sitter` or pure regex for an MVP).

**Estimated size:** ~400 production LOC + tests + grammars.

---

## 3. Better Noise Filtering

**Problem:** [`excludedTopLevelDirs`](../../../internal/context/collector.go) only contains `.git`, `node_modules`, `vendor`, `.orchestrator`. Common build directories and lockfiles are missing.

**Solution:** extend the list with common artifacts and add a filter by file extension/name.

**Scope of changes:**
- `collector.go` — `excludedTopLevelDirs` extended with:
  - `dist/`, `build/`, `target/`, `out/`
  - `.next/`, `.nuxt/`, `.cache/`, `.parcel-cache/`
  - `__pycache__/`, `.venv/`, `venv/`, `.tox/`
  - `coverage/`, `.coverage/`
  - `tmp/`, `.idea/`, `.vscode/`
- new `isNoiseFile(path string) bool` filtering:
  - lockfiles (`package-lock.json`, `yarn.lock`, `Cargo.lock`, `go.sum` — careful, `go.sum` is sometimes needed → make it configurable)
  - minified (`*.min.js`, `*.min.css`)
  - source maps (`*.map`)
- wire it into `filterInternalPaths`

**Success:**
- a `collector_test.go` test with a fixture containing all of the above → none reach `pc.Files`
- configurable through `cfg.Project.Context.ExcludePatterns` (extension of the existing config)

**External dependencies:** none.

**Estimated size:** ~50 LOC + tests.

---

## 4. Embeddings + Local Vector Store

**Problem:** file selection for context is lexical (names + imports). The query "fix the login flow" won't find `authenticate.ts` if it doesn't contain the word "login".

**Solution:** index chunks (from item 2) with embeddings and search via nearest-neighbor. Combine the result with name heuristics + graph (items 1, 2) — augment, don't replace.

**Scope of changes:**
- new package `internal/index/`
  - `type Index interface { Add(chunks []Chunk); Search(query string, k int) []Chunk; Persist(path string) error; Load(path string) error }`
  - implementation on top of [`chromem-go`](https://github.com/philippgille/chromem-go) (pure Go, in-memory + disk persistence, no server)
  - persisted in `.orchestrator/index/` (already ignored by `excludedTopLevelDirs`)
- new package `internal/embedder/`
  - `type Embedder interface { Embed(texts []string) ([][]float32, error) }`
  - implementations:
    - Ollama (`nomic-embed-text`) — default, local
    - OpenAI/Voyage — optional
- integration in `Collect()`:
  - if `cfg.Project.Context.SemanticIndex.Enabled` → build/refresh the index after `git ls-files`
  - add `pc.SemanticSearch(query string, k int) []string` returning paths
- usage in `buildCompactSourceContext`:
  - if the index is available → ranking seed comes from `Search(stagePrompt)` instead of name-based top-N
- new config section in [`internal/config/`](../internal/config/):
  ```yaml
  context:
    semantic_index:
      enabled: false
      embedder: ollama
      model: nomic-embed-text
      top_k: 20
  ```

**Success:**
- new `index_test.go` with a fixture: query "authentication" returns `login.go` even though "authentication" is not in its body
- feature flag off by default — no existing test changes behavior

**External dependencies:**
- `github.com/philippgille/chromem-go` (pure Go)
- runtime: Ollama with the `nomic-embed-text` model (optional)

**Estimated size:** ~600 production LOC + tests.

---

## 5. Merkle Cache for the Index (only with item 4)

**Problem:** without a cache, item 4 re-embeds the whole project on every run — expensive and slow.

**Solution:** a Merkle tree of file hashes. Re-index only files whose hash changed since the last run.

**Scope of changes:**
- extend `internal/index/`:
  - `type FileFingerprint struct { Path string; SHA256 string }`
  - `func BuildTree(root string, files []string) map[string]string` — hash per file
  - `func Diff(prev, curr map[string]string) (added, modified, deleted []string)`
- modify `Index.Add`:
  - on startup load the previous snapshot from `.orchestrator/index/fingerprints.json`
  - re-embed only `added + modified`
  - drop embeddings for `deleted`
- write the new snapshot after indexing finishes

**Success:**
- benchmark `index_bench_test.go`: a second `Collect()` on an unchanged repo never invokes the embedder
- integration test: modifying a single file → embedder is called for that file only

**External dependencies:** `crypto/sha256` from stdlib.

**Estimated size:** ~150 LOC + tests.

---

## Rollout Order

1. **Item 3** (noise filtering) — quick win, cleans the input for the rest
2. **Item 1** (Go graph traversal) — immediate gain for the Coder fix loop, no external dependencies
3. **Item 2** (semantic chunking) — foundation for item 4, on its own improves truncation quality
4. **Item 4** (embeddings + vector DB) — large change, opt-in via feature flag
5. **Item 5** (Merkle cache) — optimization of item 4

Items 1–3 are safe to roll out without breaking changes. Items 4–5 require a new config section and are off by default.
