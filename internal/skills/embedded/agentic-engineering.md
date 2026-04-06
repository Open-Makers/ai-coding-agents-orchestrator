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

