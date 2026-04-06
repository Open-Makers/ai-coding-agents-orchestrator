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

