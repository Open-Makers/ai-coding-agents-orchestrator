package context

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
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

// CallersOf returns repository-relative .go files that reference symbol
// (defined in defFile) in actual code — not in comments or strings, and not
// the definition itself. A candidate qualifies when it is import-connected to
// the definition:
//
//   - same package (same directory as defFile): any code reference to symbol;
//   - other package: the file imports defFile's package AND uses a selector
//     ending in symbol (package-qualified use or a method call on a value of
//     the imported type).
//
// git grep -lw is used as a fast prefilter; the AST check removes the false
// positives the old token-grep produced (comments/strings, same-named symbols
// in unrelated packages, the defining file). Empty symbol → empty result.
func CallersOf(root, defFile, symbol string) ([]string, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil, nil
	}

	candidates, err := grepWordMatches(root, symbol)
	if err != nil || len(candidates) == 0 {
		return nil, nil
	}

	defFile = filepath.ToSlash(defFile)
	defDir := path.Dir(defFile)
	modulePath := readModulePath(root)
	defPkgImport := pkgImportPath(modulePath, defDir)
	defPkgName := packageNameOf(root, defFile)

	var out []string
	for _, c := range candidates {
		c = filepath.ToSlash(c)
		if c == defFile || filepath.Ext(c) != ".go" {
			continue
		}
		if fileReferencesSymbol(root, c, symbol, defDir, defPkgImport, defPkgName) {
			out = append(out, c)
		}
	}
	return filterInternalPaths(out), nil
}

// grepWordMatches returns repo-relative files where symbol appears as a whole
// word, excluding generated/vendored top-level directories. It is only a
// prefilter; callers verify matches via the AST.
func grepWordMatches(root, symbol string) ([]string, error) {
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
	return strings.Split(trimmed, "\n"), nil
}

// fileReferencesSymbol reports whether the Go file at rel (repo-relative)
// references symbol in code, given the definition's directory, package import
// path, and package name.
func fileReferencesSymbol(root, rel, symbol, defDir, defPkgImport, defPkgName string) bool {
	abs := filepath.Join(root, rel)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil {
		return false
	}

	if path.Dir(rel) == defDir {
		// Same package: a bare or selector reference to symbol is a real use.
		return hasIdentRef(file, symbol)
	}

	// Other package: must import the definition's package.
	local, ok := importLocalName(file, defPkgImport, defPkgName)
	if !ok {
		return false
	}
	if local == "." {
		// Dot import: the symbol is referenced as a bare identifier.
		return hasIdentRef(file, symbol)
	}
	// Package-qualified use (pkg.Symbol) or a method call on a value of the
	// imported type (recv.Symbol); the import gate keeps this precise.
	return hasSelectorRef(file, symbol)
}

// hasIdentRef reports whether symbol appears as an identifier or selector
// target anywhere in the file's syntax tree (comments/strings are excluded by
// construction).
func hasIdentRef(file *ast.File, symbol string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		switch e := n.(type) {
		case *ast.SelectorExpr:
			if e.Sel != nil && e.Sel.Name == symbol {
				found = true
			}
		case *ast.Ident:
			if e.Name == symbol {
				found = true
			}
		}
		return !found
	})
	return found
}

// hasSelectorRef reports whether the file uses a selector expression whose
// selected name is symbol (e.g. pkg.Symbol or value.Symbol).
func hasSelectorRef(file *ast.File, symbol string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == symbol {
			found = true
		}
		return !found
	})
	return found
}

// importLocalName returns the local name a file uses for importPath (the
// alias, or pkgName when unaliased, or "." for a dot import) and whether the
// file imports it at all.
func importLocalName(file *ast.File, importPath, pkgName string) (string, bool) {
	if importPath == "" {
		return "", false
	}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" {
				return "", false // blank import — not referenced
			}
			return imp.Name.Name, true
		}
		return pkgName, true
	}
	return "", false
}

// pkgImportPath maps a repo-relative directory to its module import path.
func pkgImportPath(modulePath, dir string) string {
	if modulePath == "" {
		return ""
	}
	if dir == "." || dir == "" {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(dir)
}

// packageNameOf returns the package clause name of a Go file, or "".
func packageNameOf(root, rel string) string {
	abs := filepath.Join(root, rel)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, abs, nil, parser.PackageClauseOnly)
	if err != nil || file.Name == nil {
		return ""
	}
	return file.Name.Name
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
