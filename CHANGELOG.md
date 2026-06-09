# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.9.0] - 2026-06-09

### Added
- **Phased pipeline with pre-approval plan review**: the PM decomposes a task
  into ordered sub-tasks (beads) and the plan is pre-reviewed by Security and
  QA before the human approval gate (`task_plan.md`).
- **Run timing and stats** in the final summary.
- **Additional LLM backends**: GitHub Copilot CLI, LM Studio, and MLX (Apple
  Silicon) runners join OpenCode, Claude CLI, Codex CLI, and Ollama.

### Changed
- **Docs**: README updated to reflect the current 5-agent roster (PM, Coder,
  QA, UX Reviewer, Security), the full runner list, the actual
  Negotiate → Decompose → Implement (TDD) → Quality Review → Fix pipeline, and
  updated prerequisites (Go 1.26+, Git, an LLM backend, and `bd`/beads for
  resumable runs). Removed the experimental/early-stage notice.

### Removed
- **Multi-language prompts**: agent responses are now always generated in
  English; the `prompt_language` directive is a no-op.

## [0.7.0] - 2026-06-05

### Added
- **Claude Opus 4.8**: added `claude-opus-4.8` to the curated GitHub Copilot
  model list (`internal/runner/copilot.go`), available in project and global
  setup.
- **Project Memory**: new `internal/memory` package that persists project
  facts and decisions as Markdown and indexes them in a local SQLite store
  (FTS5/BM25) so new tasks automatically recall relevant past context.
  Documented in `doc/memory.md`.
- **Embedder backends**: new `internal/embedder` package with an
  OpenAI-compatible backend (`openai.go`) and a local backend
  (`cybertron.go`) for optional hybrid semantic memory search.
- **TUI reference docs**: new `doc/tui.md` documenting every panel, status
  indicator, keyboard shortcut, and token notation (`↓` input / `↑` output /
  `~` estimated) with examples.
- **Beads integration**: durable, multi-session task tracking (`.beads/`).

### Changed
- **PM/QA chat-based workflow**: replaced the rigid linear pipeline and the
  standalone `internal/orchestrator` package with a conversational flow. The
  **PM** agent now owns planning, decomposition, negotiation, and arbitration;
  the **QA** agent owns test generation, test verification, and review.
- **Agent roster** is now PM, Coder, QA, Security, and UX Reviewer. The
  Planner, Architect, Reviewer, and standalone Tester agents were removed and
  their responsibilities merged into PM and QA (deleted `architect.go`,
  `planner.go`, `reviewer.go`, and the `internal/orchestrator` package).

### Fixed
- **Go module path prompt**: entering a module path no longer returns to the
  main menu — it now resumes the originally chosen action (e.g. proceeds
  straight into the PM chat for a new task) (`internal/tui/startup.go`).

## [0.5.0] - 2026-05-18

### Added
- **Architect agent**: new pipeline role (`internal/agent/architect.go`,
  prompt `prompts/embedded/architect-system.md`, registered in
  `cmd/orchestrator/main.go`). Default skills: `agentic-engineering`,
  `architecture-decision-records`, `codebase-onboarding`.
- **MLX runner**: local Apple MLX backend (`internal/runner/mlx.go` with
  tests), wired into the runner factory and `IsLocalRunner`.
- **GitHub Copilot runner**: new `copilot` runner
  (`internal/runner/copilot.go` with tests), available via the factory.
- **Beads integration**: new `internal/beads` package for durable task
  tracking, blockers and multi-session handoff.
- **PM decomposition**: new `prompts/embedded/pm-decompose.md` and
  expanded PM agent logic (decompose / negotiate flows) with
  `pm_decompose_test.go` and `pm_negotiate_test.go`.
- **Tester update flow**: new `prompts/embedded/tester-update.md` and
  significant expansion of `internal/agent/tester.go` to update existing
  tests.
- **Coder no-op handling**: detect rewrites that produce no changes and
  skip file writes (`coder_nochanges_test.go`, refactor of `coder.go`).
- **Go declaration chunker**: new `internal/chunker/go_decl.go` for
  chunking by Go top-level declarations.
- **Runner ping**: new `internal/runner/ping.go` health-check utility for
  runners.
- **Ollama improvements**: unwrap JSON envelopes and improve plain-text
  output handling in `internal/runner/ollama.go`.
- **TUI**: substantial rework across `model.go`, `setup.go`, `sysmon.go`,
  `artifact_viewer.go`, `statusbar.go`, `styles.go`, `control.go`,
  `startup.go` and `home.go` (with updated tests).

### Changed
- **Runner factory**: `promptLanguage` argument is now ignored. All
  agents always respond in English regardless of the user's locale. The
  parameter is kept only for signature compatibility; `SkillRunner` no
  longer carries a `promptLanguage` field.
- **Orchestrator**: significant refactor of `internal/orchestrator/orchestrator.go`
  to integrate the new roles.
- **Planner**: updates to `internal/agent/planner.go`, `planner-system.md`
  and `coder-initial.md` prompts.
- **executil**: simplification of `internal/executil/command.go`.
- New `RoleArchitect` in `internal/bus/types.go`; minor updates to
  `artifacts`, `prompts/loader`, `config` and `cmd/orchestrator`.

### Removed
- **QA agent**: removed `internal/agent/qa.go`,
  `prompts/embedded/qa-system.md`, the `qa` entry in default config and
  its registration in `cmd/orchestrator/main.go`.
- **cpulimit package**: removed `internal/cpulimit/` together with all
  `cpulimit.Apply(...)` call sites. The `ReservedCores` project config
  field has been dropped from defaults.
- **omlx_claude runner**: removed `internal/runner/omlx_claude.go`
  (introduced and rolled back within this cycle).

### Breaking Changes
- Per-locale agent responses are no longer supported. Any configuration
  relying on `promptLanguage` to force a non-English response language
  will silently fall back to English.
- The `qa` agent role and its configuration entry have been removed.
  Configurations referencing `agents.qa` should be cleaned up.
- The `project.reserved_cores` configuration option has been removed.

[0.5.0]: https://github.com/Open-Makers/ai-coding-agents-orchestrator/releases/tag/v0.5.0

