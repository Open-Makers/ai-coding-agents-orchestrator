# Security Scan

Static security analysis checklist for Go projects.

## Automated Checks

```bash
# Static analysis
go vet ./...
golangci-lint run ./...

# Known vulnerabilities in dependencies
govulncheck ./...

# Race condition detection
go test -race ./...
```

## Common Vulnerabilities

### SQL Injection
```go
// BAD — user input directly in query
db.Query("SELECT * FROM users WHERE id = " + userID)

// GOOD — parameterized query
db.Query("SELECT * FROM users WHERE id = $1", userID)
```

### Command Injection
```go
// BAD — unsanitized input in command
exec.Command("sh", "-c", "echo " + userInput)

// GOOD — pass arguments directly
exec.Command("echo", userInput)
```

### Path Traversal
```go
// BAD — user controls file path
os.ReadFile(filepath.Join(baseDir, userPath))

// GOOD — clean and validate path
cleaned := filepath.Clean(userPath)
if strings.Contains(cleaned, "..") {
    return fmt.Errorf("invalid path")
}
```

### Hardcoded Secrets
- Use environment variables or secret managers
- Never commit API keys, passwords, or tokens
- Use `.gitignore` for sensitive config files

## Dependency Audit

```bash
# Check for known vulnerabilities
govulncheck ./...

# Review go.sum for unexpected changes
git diff go.sum

# List all dependencies
go list -m all
```

## Security Headers (HTTP Services)

```go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Content-Security-Policy", "default-src 'self'")
        next.ServeHTTP(w, r)
    })
}
```

