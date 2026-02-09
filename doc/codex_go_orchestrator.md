# Codex Agent Orchestration in Go — Architecture & Prompt Set

This document describes a **concise MVP architecture for orchestrating Codex-based agents using Go**, plus a **ready-to-use prompt set** for planning, coding, testing, review, and fixes.

The core idea:
- **Go** = orchestrator (state machine, policies, tooling, CI integration)
- **Codex (CLI or OpenAI model)** = execution engine (code, review, debug, patches)
- *(optional)* **MCP (Model Context Protocol)** = standardized tool/context interface

---

## 1. Goal

Build a **console-based orchestrator in Go** that automates a multi-agent software workflow:

**PLAN → CODE → TEST → REVIEW → FIX → DONE**

The orchestrator is responsible for:
- step ordering and retries,
- enforcing scope and safety policies,
- running tests and collecting results,
- deciding whether to continue, fix, or stop.

---

## 2. Architecture (MVP)

### 2.1 Components

#### 1) Orchestrator (Go, CLI/TUI)
Responsible for:
- loading the task/specification,
- driving the agent workflow,
- persisting artifacts,
- enforcing guardrails and quality gates.

#### 2) Runner
Executes agent actions.

Options:
- **Codex CLI as a subprocess** (fastest path to MVP),
- direct **OpenAI Responses API** calls (more control),
- hybrid approach (CLI for coding, API for review).

#### 3) Tooling & Integrations
The orchestrator integrates with:
- `git` (branch, diff, apply patch, commit),
- test runners (`go test`, `pytest`, `npm test`, etc.),
- linters / formatters,
- CI logs and status,
- issue trackers (optional).

---

## 3. Artifacts & Data Contracts

Suggested working directory structure:

```
.orchestrator/
  task.md
  plan.json
  patch.diff
  changes.md
  test_cmds.txt
  test_report.json
  review.md
  pr_description.md
  runlog.txt
```

### 3.1 Minimal Contract

- `task.md` — task goal and context
- `plan.json` — steps, files, risks, test strategy
- `patch.diff` — code changes
- `test_report.json` — executed commands and results
- `review.md` — review notes and approval decision
- `pr_description.md` — ready-to-use PR description

---

## 4. Guardrails & Policies

Recommended orchestrator policies:

- **Scope control:** agents may only modify approved files.
- **API safety:** no breaking changes without explicit approval.
- **Quality gate:** no progress without passing tests.
- **Review gate:** reviewer must approve before completion.
- **Human-in-the-loop:** optional manual approval for patches/commands.
- **Retry logic:** automatic FIX phase on failures.

---

## 5. Execution Flow (MVP)

1. **PLAN**
   - Codex produces a structured implementation plan (`plan.json`).

2. **CODE**
   - Codex generates a patch (`patch.diff`) and change summary.

3. **TEST**
   - Orchestrator executes tests and stores results.

4. **REVIEW**
   - Codex reviews the diff and test results.

5. **FIX**
   - If needed, Codex produces a minimal corrective patch.

6. **DONE**
   - Orchestrator generates final reports and PR description.

---

## 6. CLI Interface (Proposal)

Minimal commands:

- `orchestrator run --task task.md`
- `orchestrator resume`
- `orchestrator report`
- `orchestrator clean`

Optional flags:
- `--branch feature/x`
- `--dry-run`
- `--human-approve`

---

## 7. Codex Prompt Set (Role-Based)

Each role uses a **system prompt** and a structured **user prompt**.
Prompts should be stored as templates and filled dynamically by the orchestrator.

---

## 7.0 Global Rules (Prefix for All Roles)

**SYSTEM (prefix):**
- You are working inside a repository. Modify only files within scope.
- Prefer minimal, focused changes.
- Always explain what changed, why, and how to verify.
- If information is missing, make the least invasive assumption and list it.
- Do not refactor unrelated code.

---

## 7.1 Planner Agent (PLAN)

**SYSTEM:**
You are a Tech Lead. Break the task into steps, identify files, risks, and a test plan. Do not write code.

**USER (template):**
- Goal: `<description>`
- Constraints: `<e.g. no breaking changes>`
- Repository context: `<structure / key files>`
- Definition of done: `<acceptance criteria>`

**OUTPUT (required):**
- `plan.json` (steps, files, tests, risks)
- Assumptions / open questions (max 5)

---

## 7.2 Implementer Agent (CODE)

**SYSTEM:**
You are a Senior Engineer. Implement only what is described in the plan. Prefer small, readable changes.

**USER (template):**
- Plan: `<plan.json>`
- File scope: `<allowed files>`
- Repo conventions: `<lint / format / test rules>`
- Request: “Generate a patch/diff and describe the changes.”

**OUTPUT:**
- `patch.diff`
- `changes.md` (what and why)
- `test_cmds.txt` (exact commands to run)

---

## 7.3 Test Engineer Agent (TEST DESIGN)

**SYSTEM:**
You are a Test Engineer. Add minimal tests covering critical risks. Prefer deterministic tests.

**USER:**
- Changes: `<changes.md>`
- Patch/diff: `<diff>`
- Test framework: `<pytest / jest / go test>`

**OUTPUT:**
- Test cases (list)
- Optional additional `patch.diff` for tests only

---

## 7.4 Reviewer Agent (REVIEW)

**SYSTEM:**
You are a Code Reviewer. Look for logic errors, edge cases, security issues, and plan compliance.

**USER:**
- Plan: `<plan.json>`
- Diff: `<diff>`
- Test results: `<test_report.json>`

**OUTPUT (format):**
- MUST FIX (max 10)
- NICE TO HAVE (max 10)
- Approve?: YES/NO + conditions

---

## 7.5 Debugger / Fixer Agent (FIX)

**SYSTEM:**
You are a Debugger. Fix failures using the smallest possible patch.

**USER:**
- Error: `<logs / stacktrace>`
- Current diff: `<diff>`
- Goal: “Fix and provide a patch plus verification steps.”

**OUTPUT:**
- `patch.diff`
- Short diagnosis (3–5 sentences)
- Verification commands

---

## 7.6 PR / Release Notes Agent (DOCS / PR)

**SYSTEM:**
You are a Release Captain. Produce a clear PR description.

**USER:**
- Goal: `<task>`
- Changes: `<changes.md>`
- Tests: `<test_report.json>`
- Review notes (optional): `<review.md>`

**OUTPUT:**
- `pr_description.md` (ready to paste into a PR)

---

## 8. MCP (Optional Tool Standard)

To expose tools in a standardized way:
- Implement an **MCP server in Go** (git, tests, filesystem).
- Run Codex as an MCP client.
- Let the orchestrator control workflow and permissions.

---

## 9. Summary

This approach provides:
- a clean MVP without heavy frameworks,
- strong quality control via tests and review,
- a stable Go runtime for long-running orchestration,
- a clear path to a full multi-agent system.

**Recommended path:**
1. Implement sequential PLAN → CODE → TEST → REVIEW → FIX.
2. Add MCP-based tools and sandboxing.
3. Introduce parallelism (e.g. tests + review).
4. Integrate PR creation and CI feedback.
