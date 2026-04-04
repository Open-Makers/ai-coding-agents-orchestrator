# Implementation Plan — Codex Go Orchestrator

This plan translates the architecture in `doc/codex_go_orchestrator.md` into a concrete, staged implementation roadmap for an MVP CLI orchestrator.

## Goals
- Ship a working Go CLI that runs a sequential workflow: PLAN → CODE → TEST → REVIEW → FIX → DONE.
- Persist artifacts in `.orchestrator/` with a minimal data contract.
- Enforce simple guardrails (scope, quality gate, retry on failure).
- Support Codex CLI as the initial runner; keep API support as a later phase.

## Non-Goals (MVP)
- No parallel execution.
- No MCP server implementation.
- No CI integration beyond optional log ingestion.
- No issue tracker integration.

## Phases

### Phase 0 — Repo Scaffolding
- Add a Go module structure for the CLI app.
- Define package boundaries:
  - `cmd/` for CLI entrypoints
  - `internal/orchestrator/` for state machine
  - `internal/runner/` for Codex execution
  - `internal/artifacts/` for filesystem I/O
  - `internal/policy/` for guardrails
- Add minimal configuration model (flags + optional config file).

### Phase 1 — Artifact Contract
- Implement `.orchestrator/` workspace creation.
- Read/write:
  - `task.md`
  - `plan.json`
  - `patch.diff`
  - `changes.md`
  - `test_cmds.txt`
  - `test_report.json`
  - `review.md`
  - `pr_description.md`
  - `runlog.txt`
- Provide helper utilities for JSON serialization and file writes.

### Phase 2 — Orchestrator State Machine
- Implement sequential state machine:
  - PLAN → CODE → TEST → REVIEW → FIX → DONE
- Define a run context model:
  - task path
  - current state
  - artifact directory
  - retry counter
- Implement state transitions and exit conditions:
  - Retry FIX on test/review failures
  - Stop after max retries

### Phase 3 — Codex CLI Runner
- Implement a runner that invokes `codex` as a subprocess.
- Create prompt templates for each role:
  - Planner
  - Implementer
  - Reviewer
  - Fixer
  - PR/Release Notes (optional for MVP)
- Wire outputs to artifacts (e.g. `plan.json`, `patch.diff`, `review.md`).
- Allow dry-run to print prompts without executing Codex.

### Phase 4 — Test Execution
- Read `test_cmds.txt` and execute commands.
- Capture stdout/stderr, exit codes, and timing.
- Write `test_report.json` in a normalized format.
- Enforce quality gate: proceed only on success.

### Phase 5 — Review Gate
- Run reviewer prompt with diff and test results.
- Parse review for MUST FIX vs. approve.
- If not approved, transition to FIX.

### Phase 6 — Fix Loop
- Run fixer prompt using logs and review notes.
- Apply patch if present.
- Re-run tests, re-review, and stop after max attempts.

### Phase 7 — CLI UX
- Implement CLI commands:
  - `orchestrator run --task task.md`
  - `orchestrator resume`
  - `orchestrator report`
  - `orchestrator clean`
- Add flags:
  - `--branch`
  - `--dry-run`
  - `--human-approve`
- Add `runlog.txt` entries for each step.

### Phase 8 — Documentation & Examples
- Add usage docs and example `task.md`.
- Document artifact formats and state transitions.
- Provide example prompts and outputs.

## Risks & Mitigations
- Codex prompt drift: version prompts and pin template changes.
- Patch application failures: validate diffs before applying.
- Test command safety: allowlist commands or require approval when `--human-approve` is set.

## Deliverables
- Working CLI orchestrator that can complete at least one end-to-end run.
- Artifact directory populated with expected outputs.
- Basic error handling with meaningful exit codes.

## Definition of Done
- `orchestrator run --task task.md` executes PLAN → CODE → TEST → REVIEW → FIX → DONE.
- Artifacts are persisted in `.orchestrator/`.
- Tests gate progress and failed review triggers FIX.
- README updated with usage and artifact contract.
