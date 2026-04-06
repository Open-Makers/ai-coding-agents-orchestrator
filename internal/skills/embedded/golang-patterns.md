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

