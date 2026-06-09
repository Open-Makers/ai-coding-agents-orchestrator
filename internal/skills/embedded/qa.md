# Go Testing Patterns

Idiomatic Go testing patterns for writing reliable, maintainable tests.

## Table-Driven Tests

The standard pattern for comprehensive coverage with minimal code:

```go
func TestAdd(t *testing.T) {
    tests := []struct {
        name     string
        a, b     int
        expected int
    }{
        {"positive numbers", 2, 3, 5},
        {"negative numbers", -1, -2, -3},
        {"zero values", 0, 0, 0},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := Add(tt.a, tt.b); got != tt.expected {
                t.Errorf("Add(%d, %d) = %d; want %d", tt.a, tt.b, got, tt.expected)
            }
        })
    }
}
```

## Test Helpers

```go
func setupTestDB(t *testing.T) *sql.DB {
    t.Helper()
    db, err := sql.Open("sqlite3", ":memory:")
    if err != nil {
        t.Fatalf("failed to open database: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return db
}
```

## Interface-Based Mocking

```go
type UserRepository interface {
    GetUser(id string) (*User, error)
}

type MockUserRepository struct {
    GetUserFunc func(id string) (*User, error)
}

func (m *MockUserRepository) GetUser(id string) (*User, error) {
    return m.GetUserFunc(id)
}
```

## HTTP Handler Testing

```go
func TestHealthHandler(t *testing.T) {
    req := httptest.NewRequest(http.MethodGet, "/health", nil)
    w := httptest.NewRecorder()
    HealthHandler(w, req)

    if w.Code != http.StatusOK {
        t.Errorf("got status %d; want %d", w.Code, http.StatusOK)
    }
}
```

## Benchmarks

```go
func BenchmarkProcess(b *testing.B) {
    data := generateTestData(1000)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        Process(data)
    }
}
```

## Fuzzing

```go
func FuzzParseJSON(f *testing.F) {
    f.Add(`{"name": "test"}`)
    f.Fuzz(func(t *testing.T, input string) {
        var result map[string]any
        if err := json.Unmarshal([]byte(input), &result); err != nil {
            return
        }
        if _, err := json.Marshal(result); err != nil {
            t.Errorf("Marshal failed after Unmarshal: %v", err)
        }
    })
}
```

## Test File Organization

Test files live next to the code they test:

```
internal/
├── config/
│   ├── config.go
│   └── config_test.go
├── server/
│   ├── handler.go
│   └── handler_test.go
```

## Best Practices

- Write tests FIRST (TDD red-green-refactor).
- Use `t.Helper()` in helper functions.
- Use `t.Parallel()` for independent tests.
- Use `t.TempDir()` for temporary files.
- Test behavior through public API, not internal details.
- Test error paths, not just happy paths.
- Use meaningful test names describing the scenario.
- Avoid `time.Sleep()` — use channels or conditions.
- Use `go test -race ./...` to catch data races.
- Target 80%+ coverage for production code.

## Commands

```bash
go test ./...                           # run all tests
go test -v ./...                        # verbose output
go test -run TestAdd ./...              # specific test
go test -race ./...                     # race detector
go test -cover -coverprofile=c.out ./...# coverage
go test -bench=. -benchmem ./...        # benchmarks
go test -fuzz=FuzzParse -fuzztime=30s   # fuzzing
```



---

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



---

# Go Development Patterns

Idiomatic Go patterns and best practices.

## Core Principles

### Simplicity and Clarity
Go favors simplicity over cleverness. Code should be obvious and easy to read.

### Make the Zero Value Useful
Design types so their zero value is immediately usable without initialization.

### Accept Interfaces, Return Structs
Functions should accept interface parameters and return concrete types.

## Error Handling

- Always check errors explicitly — never ignore with `_`.
- Wrap errors with context using `fmt.Errorf("operation: %w", err)`.
- Use custom error types for domain-specific errors.
- Prefer early returns to reduce nesting.

## Package Design

- Package names: short, lowercase, no underscores.
- One package per directory.
- Organize by functionality, not by type.
- Avoid circular dependencies.
- Exported names should make sense prefixed by package name: `http.Client`, not `http.HTTPClient`.

## Project Structure

Follow standard Go layout:

```
cmd/            main applications
internal/       private packages
pkg/            public reusable packages (if needed)
```

- `internal/` prevents external imports — use it for implementation details.
- Each `cmd/<app>/main.go` should be thin — delegate to internal packages.

## Concurrency

- Use goroutines for concurrent work, channels for communication.
- Close channels from the sender side.
- Use `sync.WaitGroup` to wait on multiple goroutines.
- Protect shared data with `sync.Mutex` or `sync.RWMutex`.
- Use `context.Context` for cancellation and timeouts.
- Avoid goroutine leaks — always provide a way to stop.

## Interfaces

- Keep interfaces small and focused (1-3 methods).
- Define interfaces where they are used, not where they are implemented.
- Use standard library interfaces (`io.Reader`, `io.Writer`, `fmt.Stringer`) whenever possible.

## Struct Design

- Use value receivers for small immutable structs.
- Use pointer receivers for large structs or when mutation is needed.
- Be consistent within a type — don't mix receiver types.
- Use functional options pattern for complex constructors.

## Dependency Injection

- Accept dependencies as function/constructor parameters.
- Avoid global variables and `init()` functions.
- Use interfaces for external dependencies (database, HTTP clients).

## Naming Conventions

- `camelCase` for unexported, `PascalCase` for exported.
- Short variable names in limited scope: `r` for reader, `w` for writer.
- Descriptive names for wider scope.
- Boolean names should read as questions: `isReady`, `hasPermission`.
- Getters without `Get` prefix: `user.Name()` not `user.GetName()`.

## Common Patterns

### Functional Options
```go
type Option func(*Server)

func WithPort(port int) Option {
    return func(s *Server) { s.port = port }
}

func NewServer(opts ...Option) *Server {
    s := &Server{port: 8080}
    for _, opt := range opts {
        opt(s)
    }
    return s
}
```

### Worker Pool
```go
func process(ctx context.Context, jobs <-chan Job, workers int) {
    var wg sync.WaitGroup
    for i := 0; i < workers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for job := range jobs {
                handle(ctx, job)
            }
        }()
    }
    wg.Wait()
}
```

### Graceful Shutdown
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: ":8080"}
go func() { _ = srv.ListenAndServe() }()

<-ctx.Done()
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
_ = srv.Shutdown(shutdownCtx)
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

