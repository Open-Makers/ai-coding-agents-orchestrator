# AI Coding Agents Orchestrator

Minimal Go-based orchestrator for a PLAN → CODE → TEST → REVIEW → FIX → DONE workflow.

## Quick Start

1. Run the orchestrator with an example task:

```bash
go run ./cmd/orchestrator run --requirements doc/example_requirements.md
```

2. Review artifacts in `.orchestrator/`.

## Usage

Run a task:

```bash
go run ./cmd/orchestrator run --requirements path/to/requirements.md
```

Optional flags:
- `--branch <name>` create or checkout a branch before running
- `--dry-run` prepare workspace and exit
- `--human-approve` prompt before applying patches

Report the last run:

```bash
go run ./cmd/orchestrator report
```

Approve manual gates:

```bash
go run ./cmd/orchestrator approve architecture
go run ./cmd/orchestrator approve plan
go run ./cmd/orchestrator approve prompts
go run ./cmd/orchestrator approve all
```

Clean artifacts:

```bash
go run ./cmd/orchestrator clean
```

## How It Works (Artifacts = Results)

Each phase writes its **result** to a file in `.orchestrator/`. The orchestrator reads those results to decide what to do next, with manual approvals between architecture, plan, and prompts.

Artifacts:
- `requirements.md` — input requirements
- `architecture.md` — proposed architecture
- `architecture.approved` — manual approval marker (create empty file to approve)
- `implementation_plan.md` — implementation plan
- `plan.approved` — manual approval marker
- `prompts.md` — prompts for each implementation phase
- `prompts.approved` — manual approval marker
- `patch.diff` — code changes from CODE/FIX
- `changes.md` — summary of changes from CODE
- `test_cmds.txt` — commands to run tests
- `test_report.json` — test results from TEST
- `review.md` — review result from REVIEW
- `pr_description.md` — PR description from DONE
- `runlog.txt` — timeline of phase results and failures

## Docs
- `doc/usage.md`
- `doc/implementation_plan.md`
- `doc/prompts_go_implementation.md`
- `doc/example_task.md`
