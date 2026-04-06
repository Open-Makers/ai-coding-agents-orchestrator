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

