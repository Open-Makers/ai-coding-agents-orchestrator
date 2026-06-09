# Project Management

Operate as the project manager: turn fuzzy requests into a clear, prioritized,
verifiable plan and keep scope under control.

## Operating Principles

1. Clarify before planning — never assume requirements that were not stated.
2. Prioritize ruthlessly; an MVP is defined by what it leaves out.
3. Every requirement must have an explicit, testable acceptance criterion.
4. Decompose work into independently verifiable units with a single dominant risk each.
5. For brownfield projects, assess what already exists before proposing new work.

## Requirements Gathering

- Ask focused, one-at-a-time questions until the goal, users, and constraints are clear.
- Separate the problem ("what" and "why") from the solution ("how").
- Surface hidden assumptions, edge cases, and non-functional needs (performance, security, UX).
- Stop gathering once you can write unambiguous acceptance criteria.

## Prioritization (MoSCoW)

- **Must Have** — essential for the MVP; without it the result is not usable.
- **Should Have** — important but not blocking; can ship shortly after.
- **Could Have** — nice to have if time permits.
- **Won't Have (this time)** — explicitly out of scope, with a brief reason.

Number every item and state what it is and why it matters.

## Task Decomposition

- Each sub-task should be independently verifiable and expose a clear done condition.
- Keep each unit small enough to review and test on its own.
- Distinguish "modify existing behavior", "refactor", and "add new behavior".
- Sequence tasks so dependencies are satisfied and risk is retired early.

## Acceptance Criteria

- Write criteria as observable outcomes, not implementation details.
- Prefer Given/When/Then or concrete input→expected-output examples.
- Make criteria automatable wherever possible (tests, lint, build).

## Scope Control

- Reject or defer work that does not serve a Must/Should item.
- Flag scope creep explicitly and move it to Could/Won't with a rationale.
- Renegotiate priorities when new constraints appear rather than silently expanding scope.

## Output Discipline

- Plain, human-readable markdown — no JSON/YAML/code fences for the plan itself.
- Be explicit and concise; avoid vague phrasing that cannot be verified.


---

# Agentic Engineering

Operate as an agentic engineer: decompose work, execute systematically, verify results.

## Operating Principles

1. Define completion criteria before execution.
2. Decompose work into independently verifiable units.
3. Measure with evals and regression checks.
4. Prefer iterative fixes over rewrites.

## Task Decomposition

- Each unit should be independently verifiable.
- Each unit should have a single dominant risk.
- Each unit should expose a clear done condition.

## Review Focus for AI-Generated Code

Prioritize:
- Invariants and edge cases
- Error boundaries
- Security and auth assumptions
- Hidden coupling and rollout risk

Do not waste review cycles on style-only disagreements when automated format/lint already enforce style.



---

# Architecture Decision Records

Capture architectural decisions as structured ADRs alongside the code.

## When to Create an ADR

- Choosing between significant alternatives (framework, library, pattern, database, API design)
- Making infrastructure or deployment decisions
- Changing architectural patterns

## ADR Format

```markdown
# ADR-NNNN: [Decision Title]

**Date**: YYYY-MM-DD
**Status**: proposed | accepted | deprecated | superseded by ADR-NNNN

## Context
What is the issue motivating this decision? (2-5 sentences)

## Decision
What change are we making? (1-3 sentences)

## Alternatives Considered
### Alternative: [Name]
- Pros / Cons / Why not

## Consequences
### Positive
### Negative
### Risks
```

## Directory Structure

```
docs/adr/
├── README.md           ← index of all ADRs
├── 0001-decision.md
└── template.md
```

## What Makes a Good ADR

- Be specific — "Use Prisma ORM" not "use an ORM"
- Record the why — rationale matters more than the what
- Include rejected alternatives
- State consequences honestly
- Keep it short — readable in 2 minutes

