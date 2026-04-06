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

