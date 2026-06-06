// Package chunker splits source files into syntactic units (functions,
// types, etc.) so they can be selected whole when a file does not fit
// within a token budget. Currently only Go has structural support; other
// languages fall back to a single whole-file chunk.
package chunker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
)

// Chunk represents one syntactic unit of a source file.
type Chunk struct {
	Kind      string   // "function", "method", "type", "var", "const", "file"
	Name      string   // primary identifier (may be "" for "file" kind)
	Names     []string // all declared identifiers (grouped type/const/var blocks)
	Recv      string   // receiver type name for methods (e.g. "Foo"), else ""
	Refs      []string // type identifiers referenced in a func/method signature
	Exported  bool     // true if the primary identifier is exported (Go convention)
	StartLine int
	EndLine   int
	Body      string // verbatim source for the chunk
}

// Split returns the syntactic chunks of content. Errors only occur for
// unparseable source in supported languages; callers may treat error as
// "fall back to whole-file chunking".
func Split(path string, content []byte) ([]Chunk, error) {
	switch filepath.Ext(path) {
	case ".go":
		return chunkGo(path, content)
	default:
		return []Chunk{wholeFile(content)}, nil
	}
}

// SortByPriority orders chunks for inclusion under a tight budget:
// exported functions/methods/types first, then unexported, then const/var.
func SortByPriority(chunks []Chunk) {
	priority := func(c Chunk) int {
		base := 0
		switch c.Kind {
		case "function", "method", "type":
			base = 0
		case "const", "var":
			base = 2
		default:
			base = 1
		}
		if !c.Exported {
			base += 1
		}
		return base
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		pi, pj := priority(chunks[i]), priority(chunks[j])
		if pi != pj {
			return pi < pj
		}
		return chunks[i].StartLine < chunks[j].StartLine
	})
}

func wholeFile(content []byte) Chunk {
	return Chunk{Kind: "file", Body: string(content)}
}

func chunkGo(path string, content []byte) ([]Chunk, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		// Unparseable Go — caller gets a single whole-file chunk to fall back on.
		return []Chunk{wholeFile(content)}, nil
	}

	var chunks []Chunk
	for _, decl := range parsed.Decls {
		startPos := fset.Position(decl.Pos())
		// Start the chunk at the doc comment so a declaration's contract travels
		// with it (and is not stranded in the file header).
		if doc := declDoc(decl); doc != nil {
			startPos = fset.Position(doc.Pos())
		}
		endPos := fset.Position(decl.End())
		body := substringByOffset(content, startPos.Offset, endPos.Offset)

		kind, name := classifyGoDecl(decl)
		if kind == "" {
			continue
		}
		recv := ""
		var refs []string
		if kind == "method" || kind == "function" {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if kind == "method" {
					recv = recvTypeName(fn.Recv)
				}
				refs = signatureTypeNames(fn.Type)
			}
		}
		chunks = append(chunks, Chunk{
			Kind: kind, Name: name, Names: declNames(decl, name), Recv: recv, Refs: refs,
			Exported:  isExported(name),
			StartLine: startPos.Line, EndLine: endPos.Line, Body: body,
		})
	}

	if len(chunks) == 0 {
		return []Chunk{wholeFile(content)}, nil
	}
	return chunks, nil
}

// declDoc returns the doc comment group attached to a top-level declaration,
// or nil when there is none.
func declDoc(decl ast.Decl) *ast.CommentGroup {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return d.Doc
	case *ast.GenDecl:
		return d.Doc
	}
	return nil
}

func classifyGoDecl(decl ast.Decl) (kind, name string) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		k := "function"
		if d.Recv != nil && len(d.Recv.List) > 0 {
			k = "method"
		}
		n := ""
		if d.Name != nil {
			n = d.Name.Name
		}
		return k, n
	case *ast.GenDecl:
		switch d.Tok {
		case token.TYPE:
			return "type", firstSpecName(d.Specs)
		case token.CONST:
			return "const", firstSpecName(d.Specs)
		case token.VAR:
			return "var", firstSpecName(d.Specs)
		}
	}
	return "", ""
}

func firstSpecName(specs []ast.Spec) string {
	for _, s := range specs {
		switch sp := s.(type) {
		case *ast.TypeSpec:
			if sp.Name != nil {
				return sp.Name.Name
			}
		case *ast.ValueSpec:
			if len(sp.Names) > 0 && sp.Names[0] != nil {
				return sp.Names[0].Name
			}
		}
	}
	return ""
}

func substringByOffset(src []byte, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(src) {
		end = len(src)
	}
	if start >= end {
		return ""
	}
	return string(src[start:end])
}

// declNames returns all top-level identifiers declared by a declaration. For a
// grouped GenDecl (e.g. `type ( A; B )` or a multi-name const/var block) this
// returns every declared name so a request for any of them matches the chunk.
// Falls back to the single primary name for function/method declarations.
func declNames(decl ast.Decl, primary string) []string {
	gd, ok := decl.(*ast.GenDecl)
	if !ok {
		if primary == "" {
			return nil
		}
		return []string{primary}
	}
	var names []string
	for _, spec := range gd.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			if sp.Name != nil {
				names = append(names, sp.Name.Name)
			}
		case *ast.ValueSpec:
			for _, n := range sp.Names {
				if n != nil {
					names = append(names, n.Name)
				}
			}
		}
	}
	if len(names) == 0 && primary != "" {
		return []string{primary}
	}
	return names
}

// signatureTypeNames returns the identifier names of types referenced in a
// function/method signature (type params, parameters and results), unwrapping
// pointers, slices, maps, channels, variadics and generic instantiations.
// Package-qualified types (pkg.T) are skipped — only same-package identifiers
// are useful for pulling local type declarations into scoped context.
func signatureTypeNames(ft *ast.FuncType) []string {
	if ft == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	add := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, name := range typeIdents(f.Type) {
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	if ft.TypeParams != nil {
		add(ft.TypeParams)
	}
	add(ft.Params)
	add(ft.Results)
	return out
}

// typeIdents extracts base identifier names from a type expression.
func typeIdents(expr ast.Expr) []string {
	switch e := expr.(type) {
	case *ast.Ident:
		return []string{e.Name}
	case *ast.StarExpr:
		return typeIdents(e.X)
	case *ast.ArrayType:
		return typeIdents(e.Elt)
	case *ast.Ellipsis:
		return typeIdents(e.Elt)
	case *ast.MapType:
		return append(typeIdents(e.Key), typeIdents(e.Value)...)
	case *ast.ChanType:
		return typeIdents(e.Value)
	case *ast.IndexExpr:
		return append(typeIdents(e.X), typeIdents(e.Index)...)
	case *ast.IndexListExpr:
		out := typeIdents(e.X)
		for _, idx := range e.Indices {
			out = append(out, typeIdents(idx)...)
		}
		return out
	case *ast.ParenExpr:
		return typeIdents(e.X)
	}
	// SelectorExpr (pkg.T), FuncType, InterfaceType, StructType, etc. are
	// intentionally ignored: they are either external or not single idents.
	return nil
}

// recvTypeName returns the base type name of a method receiver, unwrapping
// pointer and generic type parameters (e.g. *Foo, Foo[T] → "Foo"). Returns ""
// when the receiver type cannot be reduced to a single identifier.
func recvTypeName(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	expr := fl.List[0].Type
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	r := name[0]
	return r >= 'A' && r <= 'Z'
}
