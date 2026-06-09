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



---

# Security Review

Analyze code for security vulnerabilities and misconfigurations.

## What to Check

### Input Validation
- Validate and sanitize all user input
- Check for SQL injection, command injection, path traversal
- Enforce length and format constraints
- Reject unexpected input types

### Authentication & Authorization
- Verify authentication on all protected endpoints
- Check authorization for resource access
- Validate tokens and session management
- Ensure secrets are not hardcoded

### Data Protection
- Sensitive data is not logged or exposed in errors
- Secrets stored in environment variables, not in code
- TLS used for network communication
- Proper file permissions set

### Dependency Security
- Check for known CVEs in dependencies
- Pin dependency versions
- Avoid pulling untrusted packages at runtime
- Review transitive dependencies

### Go-Specific Security

- Use `crypto/rand` not `math/rand` for security-sensitive randomness
- Avoid `unsafe` package unless absolutely necessary
- Use `filepath.Clean()` to prevent path traversal
- Use prepared statements for SQL queries
- Set timeouts on HTTP clients and servers
- Validate JSON input size to prevent resource exhaustion
- Use `html/template` not `text/template` for HTML output

### Command Execution
- Avoid `os/exec` with user-controlled input
- If shell commands are needed, validate and sanitize arguments
- Use absolute paths for executables
- Never pass unsanitized strings to `sh -c`

## Severity Levels

| Level | Description | Action |
|-------|-------------|--------|
| Critical | Exploitable vulnerability, data exposure | Fix immediately |
| High | Security weakness, potential for exploitation | Fix before release |
| Medium | Defense-in-depth issue | Fix in next sprint |
| Low | Best practice recommendation | Track and plan |

## Report Format

For each finding:
1. **What**: Description of the vulnerability
2. **Where**: File and line number
3. **Why**: Why it is a security risk
4. **Fix**: How to remediate it
5. **Severity**: Critical / High / Medium / Low

