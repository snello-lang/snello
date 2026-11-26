// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

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

// TestFormReach orders the blocks control can reach from the first, each
// before the blocks it goes on to: a block no edge reaches, and the
// blocks only it goes to, are left out, a loop's head reached by a back
// edge alone from a reached block is in, and a block made before one
// that goes on to it comes after it.
func TestFormReach(t *testing.T) {
	f := &Form{Blocks: []FormBlock{
		{Succs: []int{5, 3}},
		{Succs: []int{1}, Head: true}, // a loop on itself, reached from block 5
		{Succs: []int{4}},             // after a return in every arm: no edge in
		{},
		{Succs: []int{3}}, // reached only from the unreached block
		{Succs: []int{1}}, // made after block 1, which it goes on to
	}}
	f.reach(nil, nil)
	want := []int{0, 3, 5, 1}
	if !slices.Equal(f.order, want) {
		t.Errorf("order = %v, want %v", f.order, want)
	}
}
