package chunker

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestChunk_GoDecls(t *testing.T) {
	src := []byte(`package p

import "fmt"

const Pi = 3.14

var Name = "x"

type T struct {
	A int
}

func (t T) M() int { return t.A }

func Hello() string {
	return fmt.Sprint("hi")
}

func unexp() {}
`)
	chunks, err := Split("p.go", src)
	if err != nil {
		t.Fatal(err)
	}

	wantKinds := []string{"const", "var", "type", "method", "function", "function"}
	if len(chunks) != len(wantKinds) {
		t.Fatalf("expected %d chunks, got %d: %+v", len(wantKinds), len(chunks), chunks)
	}
	for i, k := range wantKinds {
		if chunks[i].Kind != k {
			t.Errorf("chunks[%d].Kind = %q, want %q", i, chunks[i].Kind, k)
		}
	}

	// Each chunk must parse standalone as a top-level Go decl.
	for _, c := range chunks {
		fset := token.NewFileSet()
		wrapped := "package p\n" + c.Body
		if _, err := parser.ParseFile(fset, "x.go", wrapped, parser.SkipObjectResolution); err != nil {
			t.Errorf("chunk %q (%s) failed to parse standalone: %v\n%s", c.Name, c.Kind, err, c.Body)
		}
	}

	// Hello() chunk should report exported.
	for _, c := range chunks {
		if c.Name == "Hello" && !c.Exported {
			t.Error("Hello must be marked exported")
		}
		if c.Name == "unexp" && c.Exported {
			t.Error("unexp must not be marked exported")
		}
	}
}

func TestChunk_NonGoFallsBack(t *testing.T) {
	chunks, err := Split("README.md", []byte("# hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Kind != "file" {
		t.Errorf("expected single file chunk, got %+v", chunks)
	}
}

func TestSortByPriority_ExportedFirst(t *testing.T) {
	src := []byte(`package p

func small() {}
func Big() {}
const c = 1
type T struct{}
`)
	chunks, _ := Split("p.go", src)
	SortByPriority(chunks)
	if chunks[0].Name != "Big" {
		t.Errorf("expected Big first after priority sort, got %q (%s)", chunks[0].Name, chunks[0].Kind)
	}
	// const should be last.
	last := chunks[len(chunks)-1]
	if last.Kind != "const" {
		t.Errorf("expected const last, got %s %s", last.Kind, last.Name)
	}
}

func TestChunk_LineRanges(t *testing.T) {
	src := []byte("package p\n\nfunc A() {}\n\nfunc B() {}\n")
	chunks, _ := Split("p.go", src)
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(chunks))
	}
	if chunks[0].StartLine != 3 || chunks[1].StartLine != 5 {
		t.Errorf("unexpected start lines: %d, %d", chunks[0].StartLine, chunks[1].StartLine)
	}
	if !strings.HasPrefix(chunks[0].Body, "func A") {
		t.Errorf("unexpected chunk body: %q", chunks[0].Body)
	}
}
