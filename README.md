# AI Coding Agents Orchestrator

Minimal Go-based orchestrator for a PLAN → CODE → TEST → REVIEW → FIX → DONE workflow.

## Requirements

- `codex` CLI available in `PATH`
- a git repository where the orchestrator can apply patches

## Installation

Build the binary:

```bash
go build -o orchestrator ./cmd/orchestrator
```

Install it into your system `PATH`:

macOS / Linux:

```bash
install ./orchestrator /usr/local/bin/orchestrator
```

Windows PowerShell:

```powershell
go build -o orchestrator.exe .\cmd\orchestrator
New-Item -ItemType Directory -Force "$HOME\bin" | Out-Null
Copy-Item .\orchestrator.exe "$HOME\bin\orchestrator.exe"
```

On Windows, make sure the target directory such as `%USERPROFILE%\bin` is added to `PATH`.

Verify installation:

```bash
orchestrator help
```

## Quick Start

1. Prepare a markdown file with requirements, for example:

```md
# Task

- Add a new CLI flag `--verbose`
- Keep backward compatibility
- Add tests for the new behavior
```

2. Run the orchestrator from the repository root:

```bash
orchestrator run --requirements path/to/requirements.md
```

3. Wait for the first approval gate and inspect `.orchestrator/architecture.md`.

4. Approve the architecture step:

```bash
orchestrator approve architecture
```

5. Repeat the same flow for the plan and prompts:

```bash
orchestrator approve plan
orchestrator approve prompts
```

6. After the approvals, the orchestrator will:
- generate a patch
- apply it to the current repository
- run tests from `.orchestrator/test_cmds.txt`
- run review and, if needed, try up to 3 fix iterations

7. Review results in `.orchestrator/`.

## Usage

Run a task:

```bash
orchestrator run --requirements path/to/requirements.md
```

Optional flags:
- `--branch <name>` create or checkout a branch before running
- `--dry-run` prepare workspace and exit
- `--human-approve` prompt before applying patches

Typical runs:

```bash
# only prepare .orchestrator/ and copy requirements
orchestrator run --requirements doc/example_requirements.md --dry-run

# run on a dedicated branch
orchestrator run --requirements doc/example_requirements.md --branch feat/orchestrated-change

# require confirmation before patch/test execution
orchestrator run --requirements doc/example_requirements.md --human-approve
```

Report the last run:

```bash
orchestrator report
```

Approve manual gates:

```bash
orchestrator approve architecture
orchestrator approve plan
orchestrator approve prompts
orchestrator approve all
```

Clean artifacts:

```bash
orchestrator clean
```

## Recommended Workflow

1. Create a focused requirements file in Markdown.
2. Start the run from the target repository root.
3. Read each generated artifact before approving it.
4. Use `report` and `.orchestrator/runlog.txt` to understand the current state.
5. Use `clean` before starting over if the workspace contains stale artifacts.

## What To Check In `.orchestrator/`

- `requirements.md` copied input used for the run
- `architecture.md` proposed solution shape
- `implementation_plan.md` execution plan for the change
- `prompts.md` prompts prepared for later implementation phases
- `patch.diff` patch produced by CODE or FIX
- `changes.md` summary of implemented changes
- `test_cmds.txt` test commands selected by the agent
- `test_report.json` test execution result
- `review.md` review outcome used to decide whether FIX is needed
- `pr_description.md` final delivery summary
- `runlog.txt` chronological trace of the workflow

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

Notes:
- `resume` exists in the CLI but is not implemented yet
- approvals are file-based markers inside `.orchestrator/`
- if tests or review fail, the orchestrator enters FIX and retries up to 3 times

## Docs
- `doc/usage.md`
- `doc/implementation_plan.md`
- `doc/prompts_go_implementation.md`
- `doc/example_task.md`
