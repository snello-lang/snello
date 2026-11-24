// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// formsSource is the program the lookups of forms.go are tested on: a
// match with alternatives, a range, a guard and a nested pattern, a
// match with no value, whose conditions are guards, and a match inside
// one of its arms, a channel of each direction, an attribute, a named
// type of the language and one of a package, and a function without a
// body.
const formsSource = `package p

import "fmt"

struct S impl Reflect { n int @fmt.Name("n") }

func f(in <-chan int, out chan<- int) Result<int, string> { return Ok(1) }

func provided(at int, n int) int

func g(x int?) int {
	return match x {
		Some(1 | 2) | None => 0,
		Some(3..=9) => 1,
		Some(n) if n > 100 | 1 > 0 => 2,
		_ => 3,
	}
}

func h(a int, b int) int {
	return match {
		a | b > 0 => match a { 4 | 5 => 1, _ => 2 },
		_ => 0,
	}
}
`

// TestPatternAt finds the | and ..= of a pattern, nested or not, in a
// match of its own inside an arm too, and no | of a guard or of a
// condition.
func TestPatternAt(t *testing.T) {
	f, file := parseForms(t)
	for at, want := range map[string]bool{
		"1 | 2":    true,
		") | None": true,
		"3..=9":    true,
		"100 | 1":  false,
		"4 | 5":    true,
		"a | b":    false,
	} {
		offset := strings.Index(formsSource, at) + strings.IndexAny(at, "|.")
		if got := patternAt(f, file.Pos(offset)); got != want {
			t.Errorf("at %q: %v, want %v", at, got, want)
		}
	}
}

// TestGuardAt finds the if of a match arm's guard and no other.
func TestGuardAt(t *testing.T) {
	f, file := parseForms(t)
	if !guardAt(f, file.Pos(strings.Index(formsSource, "if n > 100"))) {
		t.Error("the if of a guard is not found")
	}

	if guardAt(f, file.Pos(strings.Index(formsSource, "match x"))) {
		t.Error("a match is taken for a guard")
	}
}

// TestChanTypeAt finds a channel's type from either of its tokens, in
// either direction.
func TestChanTypeAt(t *testing.T) {
	f, file := parseForms(t)
	for _, at := range []string{"<-chan int", "chan int, out", "<- int)"} {
		offset := strings.Index(formsSource, at)
		if chanTypeAt(f, file.Pos(offset)) == nil {
			t.Errorf("no channel type at %q", at)
		}
	}
}

// TestIsAttr finds an attribute's @, which the tree keeps beside it.
func TestIsAttr(t *testing.T) {
	f, file := parseForms(t)
	if !isAttr(f, file.Pos(strings.Index(formsSource, "@fmt"))) {
		t.Error("the attribute's @ is not found")
	}

	if isAttr(f, file.Pos(strings.Index(formsSource, "Reflect"))) {
		t.Error("a name is taken for an attribute")
	}
}

// TestNamedTypeOf finds the named type a name writes, with its package
// when it has one, and none for a name that writes no type.
func TestNamedTypeOf(t *testing.T) {
	f, file := parseForms(t)
	result := identNamed(t, f, file, "Result")
	if nt := namedTypeOf(f, result); nt == nil || nt.Pkg != nil {
		t.Errorf("Result: %+v, want a named type with no package", nt)
	}

	if nt := namedTypeOf(f, identNamed(t, f, file, "provided")); nt != nil {
		t.Errorf("a function's name: %+v, want no named type", nt)
	}
}

// TestParamIn finds a parameter of a function without a body in its
// signature, by its place among the parameters.
func TestParamIn(t *testing.T) {
	f, file := parseForms(t)
	info := &types.Info{}
	types.Check(file, f, info)
	var fd *ast.FuncDecl
	for _, d := range f.Decls {
		if d, ok := d.(*ast.FuncDecl); ok && d.Name.Name == "provided" {
			fd = d
		}
	}

	fn, _ := info.Defs[fd.Name].(*types.Func)
	if fn == nil {
		t.Fatal("provided has no object")
	}

	sig := fn.Type().(*types.Signature)
	if p := paramIn(fd, sig, fd.Params[1].Name); p == nil || p.Name() != "n" || p.Type().String() != "int" {
		t.Errorf("the second parameter: %v, want n int", p)
	}

	if p := paramIn(fd, sig, fd.Name); p != nil {
		t.Errorf("the function's name: %v, want no parameter", p)
	}
}

// TestWordForm gives super before a dot and the blank name their
// comments, and no other name.
func TestWordForm(t *testing.T) {
	src := "package p\nfunc f(_ int) { super.M(); y := 1; print(y) }\n"
	f, file, err := parser.ParseFile("w.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	for at, want := range map[string]bool{"_ int": true, "super.M": true, "y :=": false} {
		id := identAt(f, file, strings.Index(src, at))
		if id == nil {
			t.Fatalf("no name at %q", at)
		}

		if _, got := wordForm(f, id); got != want {
			t.Errorf("at %q: %v, want %v", at, got, want)
		}
	}
}

// parseForms parses formsSource with its comments.
func parseForms(t *testing.T) (*ast.File, *token.File) {
	t.Helper()
	file := token.NewFile("forms.snello", len(formsSource))
	f, err := parser.ParseComments(file, []byte(formsSource))
	if err != nil {
		t.Fatal(err)
	}

	return f, file
}

// identNamed returns the name name at its first place in formsSource.
func identNamed(t *testing.T, f *ast.File, file *token.File, name string) *ast.Ident {
	t.Helper()
	id := identAt(f, file, strings.Index(formsSource, name))
	if id == nil || id.Name != name {
		t.Fatalf("no name %s", name)
	}

	return id
}
