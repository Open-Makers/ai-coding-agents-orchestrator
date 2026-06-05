# Project Memory

Orchestrator's project memory is inspired by [OpenClaw](https://github.com/openclaw/openclaw):
the agents do **not** maintain hidden multi-session state. Everything they
"remember" lives as plain Markdown on disk and is loaded into the prompt
on demand.

## Where it lives

```
.orchestrator/
├── memory/
│   ├── MEMORY.md              # persistent facts/decisions (pinned into every prompt)
│   ├── DREAMS.md              # optional consolidated digest (manual)
│   ├── daily/
│   │   └── YYYY-MM-DD.md      # auto-appended events per pipeline run
│   └── tasks/
│       └── <task-id>.md       # one summary per completed task
└── memory.db                  # SQLite FTS5 index (gitignored, regenerable)
```

**Commit `memory/*.md` to git.** They are the project's institutional knowledge
and should be reviewable in PRs. `memory.db` is a cache — always gitignored.

**Reset Artifacts never touches memory.** The TUI "Reset Artifacts" action
wipes generated plans/reports (`vision.md`, `architecture.md`, `changes.md`,
…) but always preserves `memory/` and `memory.db*`. Memory accumulates across
many tasks and must survive a reset — that's the whole point.

## How a new task uses it

When you run `orchestrator task --description "..."`:

1. **Reindex** — any memory file whose hash changed since the last run is
   re-chunked (≈400 tokens with 80-token overlap) and stored in SQLite.
2. **Recall** — orchestrator runs a search using the task description as the
   query and the top-K hits are formatted as a `## Project Memory` block.
3. **Pin** — the full content of `MEMORY.md` (capped at `max_pinned_chars`)
   is prepended verbatim. The result is injected into every agent's system
   prompt before `## Repository Context`.
4. **Append** — the task input is appended to today's daily log.
5. **Track** — milestones (`spec-approved`, `plan-done`) are appended to the
   daily log so progress is reconstructable even if the task crashes mid-run.
6. **Finalise** — when the pipeline reaches `done`:
   - a per-task summary is written to `memory/tasks/<task-id>.md`,
   - decision bullets (`## Decisions`, `## Constraints`, `## Principles`)
     from `architecture.md` and `vision.md` are **auto-promoted** to
     `MEMORY.md` (deduplicated by sha256 hash).
7. **On crash/abort** — a panic or returned error triggers `task-aborted`
   in the daily log + a partial `memory/tasks/<task-id>.md` summary that
   captures the failed stage, error message, and whatever artifacts existed
   at the time. Nothing is lost.

The next task starts the cycle again — the model now "remembers" everything
that came before.

## Search ranking

Default: pure **BM25** (FTS5 in SQLite) — works out of the box, no external
dependencies.

Optional: **hybrid (BM25 + cosine)** — enable with `use_embeddings: true`
and pick an embedder backend:

| `embedder`  | Notes                                                                 |
|-------------|-----------------------------------------------------------------------|
| `openai`    | POST `{base_url}/v1/embeddings`. Works with OpenAI, Voyage, Together, LM Studio, llama.cpp `--server`, vLLM. Requires `embedder_api_key` for hosted services. |
| `ollama`    | Local Ollama server at `embedder_base_url` (default `http://127.0.0.1:11434`). |
| `cybertron` | Pure-Go transformers via [`nlpodyssey/cybertron`](https://github.com/nlpodyssey/cybertron). Default model: `sentence-transformers/all-MiniLM-L6-v2` (384-dim, English). First run downloads the model (~90 MB) into `embedder_models_dir` (default `~/.orchestrator/cybertron-models`); subsequent runs are **fully offline**. CPU only. |

Final score is `α·bm25 + (1-α)·cosine`. Lower `hybrid_alpha` weights semantic
similarity more heavily.

## Configuration

In `~/.orchestrator/config.yaml` or `.orchestrator/project.yaml`:

```yaml
project:
  context:
    memory:
      enabled: true               # master switch (default true)
      auto_promote: true          # auto-add decisions to MEMORY.md (default true)
      top_k: 8                    # how many fragments to recall (default 8)
      chunk_tokens: 400           # OpenClaw default
      overlap_tokens: 80          # OpenClaw default
      hybrid_alpha: 1.0           # 1.0 = pure BM25 (default), 0.5 = balanced
      max_recall_chars: 6000      # total budget across recalled fragments
      max_pinned_chars: 4000      # MEMORY.md is truncated to this
      use_embeddings: false       # set true to enable semantic search
      embedder: openai            # | ollama | cybertron
      embedder_model: text-embedding-3-small
      embedder_base_url: ""       # for ollama / self-hosted openai
      embedder_api_key: ""        # for hosted APIs
      embedder_models_dir: ""     # cybertron model cache; default ~/.orchestrator/cybertron-models
```

### Cybertron quick start (zero external services)

```yaml
project:
  context:
    memory:
      use_embeddings: true
      hybrid_alpha: 0.5
      embedder: cybertron
      # embedder_model: sentence-transformers/all-MiniLM-L6-v2   # default
```

First memory recall after this change will download the model once
(~90 MB, requires internet). Every subsequent run is offline. No API
keys, no daemons.

## CLI

```bash
orchestrator memory show                 # print MEMORY.md
orchestrator memory search "JWT auth"    # top-K matches from memory/*.md
orchestrator memory search "auth" --k 12 --alpha 0.5
orchestrator memory reindex              # full rebuild of memory.db
orchestrator memory add "Use sqlc for typed queries"   # append fact (dedup)
orchestrator memory stats                # files / chunks / embeddings counts
```

## Editing memory by hand

`MEMORY.md` is your file. Edit it freely:

- **Add facts** as bullet points under any `##` section.
- **Reorganise** sections — order doesn't affect indexing.
- **Never delete the `<!-- hash=… -->` comments**; they prevent
  duplicates when the auto-promoter runs again.

Daily logs (`memory/daily/*.md`) are append-only by convention; the
orchestrator never rewrites them.

## How it differs from a vector DB

- All memory is **plain text** you can grep, edit, and diff.
- SQLite is **disposable** — delete `memory.db` and `orchestrator memory reindex`.
- No background daemon, no network call required for the default config.
- Works offline, in CI, in air-gapped environments.

The mental model from OpenClaw applies: *the model remembers only what is
written on disk — there is no hidden state.*
