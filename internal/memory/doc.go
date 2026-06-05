// Package memory implements OpenClaw-style persistent project memory.
//
// Memory is stored as plain Markdown files in the workspace:
//
//	.orchestrator/memory/MEMORY.md            — persistent facts/decisions (pinned)
//	.orchestrator/memory/DREAMS.md            — optional consolidated digest
//	.orchestrator/memory/daily/YYYY-MM-DD.md  — daily event log appended by the pipeline
//	.orchestrator/memory/tasks/<task-id>.md   — per-task summary
//
// Files are indexed into a local SQLite database (.orchestrator/memory.db)
// using FTS5/BM25 for fast keyword search. When an embedder is configured the
// store additionally stores dense vectors for hybrid (BM25 + cosine) ranking.
//
// The package is intentionally agnostic about which embedder backend is used;
// callers inject an embedder.Embedder via Open's options or leave it nil for
// pure BM25.
package memory
