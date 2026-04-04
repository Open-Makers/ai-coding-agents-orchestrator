# Requirements

Build a minimal Go CLI orchestrator that executes:
ARCHITECTURE → PLAN → PROMPTS → CODE → TEST → REVIEW → FIX → DONE

Constraints:
- No breaking changes.
- Prefer standard library.
- Manual approvals are required after Architecture, Plan, and Prompts.

Acceptance Criteria:
- `.orchestrator/` is created and populated with results for each phase.
- Manual approval markers gate the workflow.
- Tests gate progress.
