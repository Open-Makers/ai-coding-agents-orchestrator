# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

