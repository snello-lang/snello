// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// TestFormRefusals holds the two programs the passes refuse once they
// run on the lowered form, in the order the program evaluates them,
// which the passes over the syntax tree accept: a compound assignment
// reaches its place a second time after its value, through a borrow
// variable whose owner the value moved, which the borrow pass refuses,
// and inside the place of another assignment, which the move pass
// refuses. The move pass runs on the form beside the tree's, so its
// refusal is reported as the comparison's disagreement; the borrow
// pass's row is skipped until that pass runs on the form, when both
// rows join TestAssignOrder and testdata/errors.
func TestFormRefusals(t *testing.T) {
	const decls = "struct B { n int; name string }\nfunc pick2(b mut B, k int, m int) mut B { return b }\n" +
		"func takenXs(xs []int) int { return 0 }\nfunc drained(v []string) string { return \"\" }\n"
	tests := []struct {
		src  string
		want string
		skip string
	}{
		{"func f(v []string) { r := mut v[0]; *r += drained(v) }", "cannot move v: v is borrowed by r", "refused once the borrow pass runs on the lowered form"},
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; xs := []int{1}; pick2(mut b, if c { xs[0] += takenXs(xs); 2 } else { 3 }, 0).n = 1 }", "use of moved value xs", ""},
	}
	for _, tt := range tests {
		if tt.skip != "" {
			t.Log(tt.skip)
			continue
		}

		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != 1 || !strings.Contains(got[0], tt.want) {
			t.Errorf("%s:\n  got %q\n  want one containing %q", tt.src, got, tt.want)
		}
	}
}

// TestFormString prints a form built by hand: a borrow into a
// temporary, a call, a store, a loop with a head and a back edge, and a
// leave with its drops.
func TestFormString(t *testing.T) {
	xs, r := NewVar(token.NoPos, "xs", NewSlice(Typ[Int])), NewVar(token.NoPos, "r", NewRef(Typ[Int], true))
	f := &Form{Vars: []*Var{xs, r}, Temps: 2}
	f.Instrs = []Instr{
		{Op: OpBorrow, Place: Operand{Var: 0, Path: "[]"}, To: Temp(0), Mut: true},
		{Op: OpStore, Place: Operand{Var: 1}, To: Temp(0)},
		{Op: OpRead, Place: Operand{Var: 1, Path: "*"}},
		{Op: OpCall, To: Temp(1), Args: []Operand{{Var: 0}, Temp(0)}},
		{Op: OpStore, Place: Operand{Var: 0, Path: "[]"}, To: Temp(1)},
		{Op: OpLeave, Args: []Operand{{Var: 1}, {Var: 0}}},
	}
	f.Blocks = []FormBlock{
		{From: 0, To: 2, Succs: []int{1}},
		{From: 2, To: 3, Succs: []int{2, 3}, Head: true},
		{From: 3, To: 5, Succs: []int{1}},
		{From: 5, To: 6},
	}
	want := `b0:
  t0 = borrow mut xs.[]
  store r <- t0
  -> b1
b1 (head):
  read r.*
  -> b2 b3
b2:
  t1 = call(xs, t0)
  store xs.[] <- t1
  -> b1
b3:
  leave(r, xs)
  ->
`
	if got := f.String(); got != want {
		t.Errorf("printed:\n%s\nwant:\n%s", got, want)
	}
}
