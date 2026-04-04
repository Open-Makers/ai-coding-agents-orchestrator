# Orchestrator Usage

## Commands
- `orchestrator run --task task.md` — run the full workflow
- `orchestrator report` — print summary from `.orchestrator/`
- `orchestrator clean` — remove `.orchestrator/`

## Flags
- `--branch <name>`: create/checkout branch before running
- `--dry-run`: prepare workspace and exit
- `--human-approve`: prompt before applying patches

## Artifacts
- `.orchestrator/task.md`
- `.orchestrator/plan.json`
- `.orchestrator/patch.diff`
- `.orchestrator/changes.md`
- `.orchestrator/test_cmds.txt`
- `.orchestrator/test_report.json`
- `.orchestrator/review.md`
- `.orchestrator/pr_description.md`
- `.orchestrator/runlog.txt`
