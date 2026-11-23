// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/platform"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
	"github.com/snello-lang/snello/std"
)

// pointSrc is a program whose declarations the view's tests ask about.
const pointSrc = `package main

// A Point is a place on the plane.
struct Point {
	x int // the abscissa
	y int
}

// Origin is the point at zero.
func Point.Origin() Point { return Point{x: 0, y: 0} }

func main() {
	p := Point.Origin()
	_ = p.x == "a" + "b"
}
`

// TestIdentAt finds the name whose letters cover an offset, or else the
// one that ends there; and none away from a name.
func TestIdentAt(t *testing.T) {
	f, file := parse(t, pointSrc)
	tests := []struct {
		lead, want string
	}{
		{"p := Po", "Point"},        // within
		{"p := Point", "Point"},     // at its end, before the dot
		{"p := Point.", "Origin"},   // at its start
		{"Point{x: 0, y: 0} ", ""},  // a space after a brace
		{"x int // the a", ""},      // in a comment
		{"_ = p.x == \"a\" + ", ""}, // a literal, not a name
	}

	for _, tt := range tests {
		id := identAt(f, file, offset(t, pointSrc, tt.lead))
		got := ""
		if id != nil {
			got = id.Name
		}

		if got != tt.want {
			t.Errorf("after %q: %q, want %q", tt.lead, got, tt.want)
		}
	}
}

// TestTokenAt finds the stretch of the token at an offset: an operator,
// a string with its quotes, a name; and none in a comment or past the
// text.
func TestTokenAt(t *testing.T) {
	tests := []struct {
		lead, want string
	}{
		{"_ = p.x ", "=="},
		{"_ = p.x =", "=="},
		{"_ = p.x == ", `"a"`},
		{"_ = p.x == \"a", `"a"`},
		{"p := ", "Point"},
		{"x int // ", ""},
	}

	for _, tt := range tests {
		start, end, ok := tokenAt("point.snello", []byte(pointSrc), offset(t, pointSrc, tt.lead))
		got := ""
		if ok {
			got = pointSrc[start:end]
		}

		if got != tt.want {
			t.Errorf("after %q: %q, want %q", tt.lead, got, tt.want)
		}
	}

	if _, _, ok := tokenAt("point.snello", []byte(pointSrc), len(pointSrc)+1); ok {
		t.Error("a token past the text")
	}
}

// TestOwner finds the innermost node that records a token at a position
// as its own: the binary expression of an operator, the call of its
// parenthesis; and none for a position no node records.
func TestOwner(t *testing.T) {
	f, file := parse(t, pointSrc)
	at := func(lead string) token.Pos { return file.Pos(offset(t, pointSrc, lead)) }
	if b, ok := owner(f, at("_ = p.x ")).(*ast.BinaryExpr); !ok || b.Op.String() != "==" {
		t.Errorf("the ==: %T, want its binary expression", owner(f, at("_ = p.x ")))
	}

	if b, ok := owner(f, at("_ = p.x == \"a\" ")).(*ast.BinaryExpr); !ok || b.Op.String() != "+" {
		t.Errorf("the +: %T, want the inner binary expression", owner(f, at("_ = p.x == \"a\" ")))
	}

	if _, ok := owner(f, at("p := Point.Origin")).(*ast.CallExpr); !ok {
		t.Errorf("the call's parenthesis: %T, want the call", owner(f, at("p := Point.Origin")))
	}

	if n := owner(f, at("_ = p.x == \"a\" +")); n != nil {
		t.Errorf("a space: %T, want none", n)
	}
}

// TestDeclares tells the node that declares a name from one that does
// not: a type's, a function's, a field's.
func TestDeclares(t *testing.T) {
	f, file := parse(t, pointSrc)
	decl := f.Decls[0].(*ast.TypeDecl)
	field := decl.Def.(*ast.StructType).Fields[0]
	fn := f.Decls[1].(*ast.FuncDecl)
	tests := []struct {
		n    ast.Node
		lead string
		want bool
	}{
		{decl, "struct ", true},
		{field, "\t", true},
		{fn, "func Point.", true},
		{fn, "func ", false}, // the type it belongs to, not its name
		{field, "\tx ", false},
	}

	for _, tt := range tests {
		if got := declares(tt.n, file, offset(t, pointSrc, tt.lead)); got != tt.want {
			t.Errorf("%T after %q: %v, want %v", tt.n, tt.lead, got, tt.want)
		}
	}
}

// TestSource reads a file as the check read it: through the server, a
// standard package's from the compiler, by its path under std/, and any
// other from the disk.
func TestSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.snello")
	if err := os.WriteFile(path, []byte("on the disk"), fileMode); err != nil {
		t.Fatal(err)
	}

	v := newView(map[string][]byte{"/read/b.snello": []byte("as read")})
	want, err := std.Sources.ReadFile("fmt/fmt.snello")
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"/read/b.snello": "as read", "fmt/fmt.snello": string(want), path: "on the disk"} {
		if got, err := v.source(name); err != nil || string(got) != want {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}

	if _, err := v.source(filepath.Join(t.TempDir(), "none.snello")); err == nil {
		t.Error("a file that is not there")
	}
}

// TestDeclaration writes a function of a type with the type it belongs
// to, and its doc comment; a field with its line comment, which it has
// in place of a doc comment; and a local with neither.
func TestDeclaration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "point.snello")
	if err := os.WriteFile(path, []byte(pointSrc), fileMode); err != nil {
		t.Fatal(err)
	}

	v, o := New(nil, io.Discard, io.Discard).begin(nil, platform.Host())
	if _, _, err := importer.LoadProgramFrom(o.Load([]string{path}), v.info); err == nil || !strings.Contains(err.Error(), "mismatched") {
		t.Fatalf("the check: %v, want the one error of the comparison", err)
	}

	v.trim(map[string]*token.File{path: v.files[0]})
	objs := map[string]types.Object{}
	for id, obj := range v.info.Defs {
		if obj != nil {
			objs[id.Name] = obj
		}
	}

	tests := []struct {
		name, decl, doc string
	}{
		{"Origin", "func Point.Origin() Point", "Origin is the point at zero.\n"},
		{"Point", "struct Point {\n\tx int\n\ty int\n}", "A Point is a place on the plane.\n"},
		{"x", "field x int", "the abscissa\n"},
		{"p", "var p Point", ""},
	}

	for _, tt := range tests {
		if decl, doc := v.declaration(objs[tt.name]); decl != tt.decl || doc != tt.doc {
			t.Errorf("%s: %q, %q, want %q, %q", tt.name, decl, doc, tt.decl, tt.doc)
		}
	}
}

// parse parses src as the file point.snello.
func parse(t *testing.T, src string) (*ast.File, *token.File) {
	t.Helper()
	file := token.NewFile("point.snello", len(src))
	f, err := parser.Parse(file, []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	return f, file
}

// offset is the offset of the end of lead in src.
func offset(t *testing.T, src, lead string) int {
	t.Helper()
	i := strings.Index(src, lead)
	if i < 0 {
		t.Fatalf("no %q", lead)
	}

	return i + len(lead)
}
