# Prompts — Go Implementation (Single Document)

This document provides a single, consolidated prompt set for implementing the orchestrator in Go, aligned with `doc/implementation_plan.md` phases.

## Global Rules (Prefix for All Phases)
SYSTEM:
- You are working inside a repository. Modify only files within scope.
- Prefer minimal, focused changes.
- Always explain what changed, why, and how to verify.
- If information is missing, make the least invasive assumption and list it.
- Do not refactor unrelated code.
- Use idiomatic Go and standard library where possible.
- Keep CLI behavior predictable; avoid hidden side effects.

## Phase 0 — Repo Scaffolding
SYSTEM:
You are setting up a Go CLI project structure. Keep it minimal and idiomatic. Do not implement business logic.

USER (template):
- Goal: "Create initial Go CLI scaffolding for the orchestrator."
- Constraints: `<e.g. no new dependencies>`
- Repository context: `<current tree>`
- Definition of done: "Module builds and has CLI entrypoint."

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 1 — Artifact Contract
SYSTEM:
You are implementing artifact storage and filesystem I/O. Keep the contract identical to the spec. Do not add orchestration logic.

USER (template):
- Goal: "Implement .orchestrator/ artifact contract and helpers."
- Contract: `<list of files>`
- File scope: `<allowed files>`
- Repo conventions: `<lint / format / test rules>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 2 — Orchestrator State Machine
SYSTEM:
You are implementing a sequential state machine (PLAN → CODE → TEST → REVIEW → FIX → DONE) with retry logic. Keep it deterministic.

USER (template):
- Goal: "Implement state machine and run context."
- Constraints: `<max retries, stop conditions>`
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 3 — Codex CLI Runner
SYSTEM:
You are implementing a runner that invokes Codex CLI as a subprocess and writes outputs to artifacts. Do not add network API calls.

USER (template):
- Goal: "Implement Codex CLI runner with prompt templates."
- Prompt roles: `planner, implementer, reviewer, fixer, docs`
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 4 — Test Execution
SYSTEM:
You are implementing test command execution with structured reports and strict gating. No parallelism.

USER (template):
- Goal: "Execute test_cmds.txt and store test_report.json."
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 5 — Review Gate
SYSTEM:
You are implementing review prompt execution and approval parsing. If not approved, transition to FIX.

USER (template):
- Goal: "Run review, parse MUST FIX vs approve, and gate progress."
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 6 — Fix Loop
SYSTEM:
You are implementing the fix loop: run fixer, apply patch, re-test, re-review, respect max retries.

USER (template):
- Goal: "Implement fix loop with retries."
- Constraints: `<max retries>`
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 7 — CLI UX
SYSTEM:
You are implementing CLI commands and flags. Keep behavior explicit and documented.

USER (template):
- Goal: "Add CLI commands run/resume/report/clean and flags."
- Constraints: `<behavior rules>`
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`

## Phase 8 — Documentation & Examples
SYSTEM:
You are writing documentation and examples. Do not change code.

USER (template):
- Goal: "Add usage docs and example task.md."
- File scope: `<allowed files>`

OUTPUT:
- `patch.diff`
- `changes.md`
- `test_cmds.txt`
