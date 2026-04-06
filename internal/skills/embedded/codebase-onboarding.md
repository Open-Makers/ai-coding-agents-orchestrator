# Codebase Onboarding

How to understand and navigate an existing codebase quickly.

## First Steps

1. Read `README.md` and any `doc/` documentation
2. Check `go.mod` for module path and dependencies
3. Look at `cmd/` for entry points
4. Examine `internal/` for core business logic
5. Run `go build ./...` and `go test ./...` to verify state

## Understand the Structure

```
cmd/          → entry points (main packages)
internal/     → private implementation packages
pkg/          → public reusable packages (if present)
doc/          → documentation
```

## Key Questions

- What does this project do?
- What are the main entry points?
- What external dependencies does it use?
- What is the test coverage?
- How is configuration handled?
- How is error handling done?

## Code Navigation

- Start from `main()` and trace the call chain
- Identify core interfaces and their implementations
- Find configuration loading and validation
- Locate error handling patterns
- Check for middleware or interceptors

## Before Making Changes

- Understand the existing patterns and follow them
- Run existing tests to establish a baseline
- Check for linting rules (`golangci-lint`)
- Review recent git history for context

