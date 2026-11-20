// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast_test

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
)

// TestSource prints expressions as a program writes them, which is the
// text a failed expectation shows.
func TestSource(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"n > limit", "n > limit"},
		{"expect.Equal(ref Describe(c.kind), ref c.want)", "expect.Equal(ref Describe(c.kind), ref c.want)"},
		{"xs[1:3][0] + -y", "xs[1:3][0] + -y"},
		{"Case{name: \"a\", in: []int{1, 2}}", "Case{name: \"a\", in: []int{1, 2}}"},
		{"take(mut s.head) ?? return None", "take(mut s.head) ?? return None"},
		{"f(x)? == 3", "f(x)? == 3"},
		{"func(a int) bool { return a > 0 }", "func(a int) bool { ... }"},
		{"arena.New<Node>()", "arena.New<Node>()"},
		{"match s { Closed => 1, _ => 2 }", "match s { ... }"},
		{"m[k] ?? 0", "m[k] ?? 0"},
	} {
		f, _, err := parser.ParseFile("t.snello", []byte("package p\nvar x = "+c.src+"\n"))
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}

		if got := ast.Source(f.Decls[0].(*ast.VarDecl).Values[0]); got != c.want {
			t.Errorf("%s: got %q, want %q", c.src, got, c.want)
		}
	}
}

// TestWriteSignature writes functions' declarations without their
// bodies, every part a signature has.
func TestWriteSignature(t *testing.T) {
	for _, c := range []string{
		"func Area() int",
		"func (b mut Builder) WriteString(s ref string)",
		"func Point.Parse(s ref string) Point?",
		"func Map<K: Hash + Eq, V>(m ref map[K]V, f func(V) V) map[K]V",
		"func Printf(format ref string, args ...ref any)",
		"func Split(s ref string) (string, string)",
		"func (l ref List<T>) Len() int",
	} {
		f, _, err := parser.ParseFile("t.snello", []byte("package p\n"+c+" {}\n"))
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}

		var b strings.Builder
		ast.WriteSignature(&b, f.Decls[0].(*ast.FuncDecl))
		if got := b.String(); got != c {
			t.Errorf("got %q, want %q", got, c)
		}
	}
}

// TestSourceVariadic holds a function type's variadic parameter to its
// ..., which a parameter list once dropped.
func TestSourceVariadic(t *testing.T) {
	f, _, err := parser.ParseFile("t.snello", []byte("package p\nvar f func(args ...int)\n"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := ast.SourceType(f.Decls[0].(*ast.VarDecl).Type), "func(args ...int)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestWriteParams writes a variant's fields as written.
func TestWriteParams(t *testing.T) {
	f, _, err := parser.ParseFile("t.snello", []byte("package p\nenum Shape { Circle(r f64, tags ...string) }\n"))
	if err != nil {
		t.Fatal(err)
	}

	v := f.Decls[0].(*ast.TypeDecl).Def.(*ast.EnumType).Variants[0]
	var b strings.Builder
	ast.WriteParams(&b, v.Fields)
	if got, want := b.String(), "r f64, tags ...string"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
