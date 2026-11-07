// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ast_test

import (
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
