// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// TestFormRefusals holds the programs the passes refuse once they run
// on the lowered form, in the order the program evaluates them, which
// the passes over the syntax tree accept: a compound assignment reaches
// its place a second time after its value, through a borrow variable
// whose owner the value moved, or whose owner the value borrows mut
// again, which the borrow pass refuses, and inside the place of another
// assignment, which the move pass refuses; and a borrow of an arm's
// variable carried out of the arm's block by an if expression and given
// to a call, which the tree lends only once the block is closed. Each
// pass runs on the form beside the tree's, so its refusal is reported
// as the comparison's disagreement until the form's passes report in
// the tree's place, when the rows join TestAssignOrder and
// testdata/errors.
func TestFormRefusals(t *testing.T) {
	const decls = "struct B { n int; name string }\nfunc pick2(b mut B, k int, m int) mut B { return b }\n" +
		"func takenXs(xs []int) int { return 0 }\nfunc drained(v []string) string { return \"\" }\nfunc growB(b mut B) int { return 1 }\n" +
		"func pass(a ref string, b int) {}\nfunc cond() bool { return true }\n"
	tests := []struct {
		src  string
		want string
	}{
		{"func f(v []string) { r := mut v[0]; *r += drained(v) }", "cannot move v: v is borrowed by r"},
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; xs := []int{1}; pick2(mut b, if c { xs[0] += takenXs(xs); 2 } else { 3 }, 0).n = 1 }", "use of moved value xs"},
		{"func f() { b := B{n: 0, name: \"b\"}; r := mut b; r.n += growB(mut b) }", "cannot borrow b as mut: b is already borrowed as mut by r"},
		{"func f() { z := \"z\"; pass(if cond() { t := \"b\".Clone(); ref t } else { ref z }, 1) }", "cannot keep a borrow of t: t is dropped at the end of its block"},
	}
	for _, tt := range tests {
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
	f := &Form{Vars: []*Var{xs, r}, Temps: make([]TempInfo, 2)}
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

// TestFormReach marks the blocks control can reach from the first: a block
// no edge reaches, and the blocks only it goes to, are not reached, while
// a loop's head reached by a back edge alone from a reached block is.
func TestFormReach(t *testing.T) {
	f := &Form{Blocks: []FormBlock{
		{Succs: []int{1, 3}},
		{Succs: []int{1}, Head: true}, // a loop on itself, reached from the first
		{Succs: []int{4}},             // after a return in every arm: no edge in
		{},
		{Succs: []int{3}}, // reached only from the unreached block
	}}
	f.reach(nil)
	want := []bool{true, true, false, true, false}
	for b, ok := range want {
		if f.reached[b] != ok {
			t.Errorf("block %d reached = %v, want %v", b, f.reached[b], ok)
		}
	}
}
