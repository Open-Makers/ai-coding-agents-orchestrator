# TDD Workflow

Test-driven development with the red-green-refactor cycle.

## The Cycle

```
RED     → Write a failing test first
GREEN   → Write minimal code to pass the test
REFACTOR → Improve code while keeping tests green
REPEAT  → Continue with next requirement
```

## Step-by-Step

### 1. Write Failing Test (RED)
```go
func TestCalculate(t *testing.T) {
    got := Calculate(10, 5)
    want := 15
    if got != want {
        t.Errorf("Calculate(10, 5) = %d; want %d", got, want)
    }
}
```

### 2. Run Test — Verify FAIL
```bash
go test ./...
# --- FAIL: TestCalculate
```

### 3. Implement Minimal Code (GREEN)
```go
func Calculate(a, b int) int {
    return a + b
}
```

### 4. Run Test — Verify PASS
```bash
go test ./...
# PASS
```

### 5. Refactor
Improve code quality while keeping tests green:
- Remove duplication
- Improve naming
- Optimize performance
- Enhance readability

### 6. Verify Coverage
```bash
go test -cover ./...
# Target: 80%+ coverage
```

## Coverage Requirements

- Minimum 80% coverage (unit + integration)
- All edge cases covered
- Error scenarios tested
- Boundary conditions verified

## Test Types

### Unit Tests
- Individual functions and methods
- Pure functions
- Helpers and utilities
- In the same package as the code

### Integration Tests
- API endpoints
- Database operations
- Service interactions
- External dependencies (via interfaces)

## Best Practices

1. **Tests BEFORE code** — always write the test first
2. **One assert per test** — focus on single behavior
3. **Descriptive names** — explain what scenario is tested
4. **Arrange-Act-Assert** — clear test structure
5. **Mock external deps** — isolate unit tests via interfaces
6. **Test edge cases** — nil, empty, large, negative
7. **Test error paths** — not just happy paths
8. **Keep tests fast** — unit tests should run in milliseconds
9. **Clean up** — use `t.Cleanup()` for resource teardown
10. **No skipped tests** — fix or remove disabled tests

