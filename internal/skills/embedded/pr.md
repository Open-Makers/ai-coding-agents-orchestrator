# Git Workflow

Git best practices for the orchestrator's automated workflow.

## Commit Messages

Use conventional commits format:

```
<type>(<scope>): <subject>

[optional body]
```

### Types
- `feat`: new feature
- `fix`: bug fix
- `refactor`: code restructuring without behavior change
- `test`: adding or updating tests
- `docs`: documentation changes
- `chore`: maintenance tasks

### Examples
```
feat(auth): add JWT token validation
fix(api): handle nil pointer in user lookup
refactor(config): extract validation into separate function
test(handler): add table-driven tests for CreateUser
```

## Branching

- `main` — always deployable, protected
- `feature/<name>` — new features, branched from main
- `fix/<name>` — bug fixes

## Pull Request Description

Include:
1. **What** changed and why
2. **Testing** — how was it verified
3. **Breaking changes** — if any
4. **Related issues** — link to tickets

## Best Practices

- Keep commits atomic — one logical change per commit
- Write meaningful commit messages explaining why, not what
- Rebase feature branches before merging to keep history clean
- Never force-push to shared branches
- Review diffs before committing

