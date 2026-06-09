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



---

# Verification Loop

Iterative build-test-fix cycle for generated code.

## The Loop

```
GENERATE → BUILD → FIX → TEST → FIX → DONE
```

1. Generate all source files first
2. Run build (`go build ./...`)
3. If build fails — fix all compilation errors
4. Run tests (`go test ./...`)
5. If tests fail — fix failing tests
6. Repeat until green

## Build Fix Strategy

When fixing build errors:
- Read the full error output carefully
- Fix ALL errors at once, not one at a time
- Check import paths match the module in `go.mod`
- Verify function signatures match their usage
- Ensure types are consistent across packages

## Common Build Errors

### Wrong Import Path
```
package path/to/module is not in std
```
Fix: Use the module path from `go.mod`, e.g. `github.com/user/project/internal/pkg`

### Undefined Reference
```
undefined: SomeFunction
```
Fix: Check the function is exported (PascalCase) and the import is correct

### Type Mismatch
```
cannot use x (type A) as type B
```
Fix: Ensure consistent types across function boundaries

## Rules

- Write ALL files before attempting to build
- Include `go.mod` when creating a new project
- Never use relative imports (`./pkg`) — use full module path
- Run `go mod tidy` to fix dependency issues
- Fix errors iteratively until build succeeds



---

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

