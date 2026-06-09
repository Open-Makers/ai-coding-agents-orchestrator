# UX/UI Review

Analyze code for user experience quality, accessibility, and usability.

## What to Check

### Error Messages & Feedback
- Error messages are clear and actionable
- Success/failure states are communicated to the user
- Progress indicators for long-running operations
- No raw stack traces or internal errors exposed to users

### Input Handling
- Input validation with helpful, specific error messages
- Sensible defaults for optional parameters
- Consistent input formats across the application

### CLI/API Ergonomics
- Flag/option names are intuitive and consistent
- Help text is complete and includes examples
- Output formatting is readable (tables, JSON, colors)
- Exit codes follow conventions (0=success, 1=error, 2=usage)

### Accessibility
- Keyboard navigation support
- Screen reader compatibility (ARIA labels where applicable)
- Sufficient color contrast
- No color-only information encoding

### Consistency
- Terminology is consistent throughout
- Naming conventions are uniform
- Behavior is predictable across similar operations

### Graceful Degradation
- Fallback behavior when optional features are unavailable
- Meaningful behavior with minimal configuration
- Timeout handling with user-visible feedback

## Response Format

```
MUST FIX
- <critical UX issue>

NICE TO HAVE
- <improvement suggestion>

Approve?: YES or NO
```



---

# Coding Standards

Code quality standards for generated code.

## General Rules

- Write complete, working, compilable code
- No placeholder comments like "// TODO: implement"
- No stub functions — implement the real logic
- Handle all errors explicitly
- Use consistent formatting (`gofmt`)

## Go-Specific Standards

### Imports
- Group imports: stdlib, external, internal
- Use `goimports` ordering
- No unused imports

### Error Handling
```go
// Always wrap errors with context
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}
```

### Naming
- Exported: `PascalCase`
- Unexported: `camelCase`
- Constants: `PascalCase` or `camelCase` depending on export
- Acronyms: `HTTP`, `ID`, `URL` (all caps when exported)

### Function Design
- Functions do one thing
- Keep functions short (< 40 lines preferred)
- Use early returns to reduce nesting
- Accept interfaces, return concrete types

### Project Layout
- `cmd/<app>/main.go` — thin entry point
- `internal/<package>/` — implementation by domain
- Tests next to code: `foo.go` → `foo_test.go`
- No `src/` directory — Go projects don't use `src/`

## Code Review Checklist

- [ ] Compiles without errors
- [ ] Tests pass
- [ ] No hardcoded values that should be configurable
- [ ] Error messages are helpful
- [ ] No data races (use `-race` flag)
- [ ] Resources are properly closed (files, connections)
- [ ] No security vulnerabilities (injection, path traversal)

