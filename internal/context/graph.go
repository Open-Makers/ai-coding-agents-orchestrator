package context

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/Open-Makers/ai-coding-agents-orchestrator/internal/executil"
)

// ImportsOf returns repository-relative paths of files imported by the given
// file, restricted to the repository's own module. Stdlib and third-party
// imports are skipped because they live outside the repo. For non-Go files
// the function returns (nil, nil).
func ImportsOf(root, file string) ([]string, error) {
	if filepath.Ext(file) != ".go" {
		return nil, nil
	}
	abs := filepath.Join(root, file)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, abs, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	modulePath := readModulePath(root)
	if modulePath == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, imp := range parsed.Imports {
		raw := strings.Trim(imp.Path.Value, `"`)
		rel, ok := relativeRepoPath(modulePath, raw)
		if !ok {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, rel, "*.go"))
		if err != nil {
			continue
		}
		for _, m := range matches {
			r, err := filepath.Rel(root, m)
			if err != nil {
				continue
			}
			r = filepath.ToSlash(r)
			if r == file {
				continue
			}
			if _, dup := seen[r]; dup {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	return filterInternalPaths(out), nil
}

// CallersOf returns repository-relative paths that mention the symbol as a
// whole word, via `git grep -lw`. Empty symbol → empty result.
func CallersOf(root, symbol string) ([]string, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil, nil
	}
	args := []string{"grep", "-lw", "--", symbol}
	for _, dir := range excludedTopLevelDirs {
		name := strings.TrimSuffix(dir, "/")
		args = append(args,
			":(exclude,glob)"+name+"/**",
			":(exclude,glob)**/"+name+"/**",
		)
	}
	cmd := executil.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// `git grep` exits non-zero when no matches; treat as empty.
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return filterInternalPaths(strings.Split(trimmed, "\n")), nil
}

// PrimarySymbolOf returns a best-effort exported symbol from a Go file,
// used as the seed for CallersOf. Returns "" when no exported symbol exists.
func PrimarySymbolOf(root, file string) string {
	if filepath.Ext(file) != ".go" {
		return ""
	}
	abs := filepath.Join(root, file)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil {
		return ""
	}
	for _, decl := range parsed.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				return d.Name.Name
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
					return ts.Name.Name
				}
			}
		}
	}
	return ""
}

func readModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), `"`)
		}
	}
	return ""
}

func relativeRepoPath(modulePath, importPath string) (string, bool) {
	if importPath == modulePath {
		return ".", true
	}
	prefix := modulePath + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(importPath, prefix), true
}
