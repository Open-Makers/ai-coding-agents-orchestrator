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

