// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/types"
)

// TestVariable finds the variable an expression names, through
// parentheses, and nothing for a field, an element or a call.
func TestVariable(t *testing.T) {
	f, file, err := parser.ParseFile("t.snello", []byte(`package main
struct Box { n int }
func main() {
	b := Box{n: 1}
	xs := []int{1}
	_ = b
	_ = (b)
	_ = b.n
	_ = xs[0]
	_ = len(xs)
}`))
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{}
	if _, err := (&types.Config{}).Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	body := f.Decls[1].(*ast.FuncDecl).Body.List
	wants := []string{"", "", "b", "b", "", "", ""}
	for i, s := range body[2:] {
		x := s.(*ast.AssignStmt).Rhs[0]
		got := ""
		if v := Variable(info, x); v != nil {
			got = v.Name()
		}

		if got != wants[i+2] {
			t.Errorf("statement %d: Variable = %q, want %q", i+2, got, wants[i+2])
		}
	}
}
