// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// TestWithin checks which places a record covers: the place itself and
// the places inside it, by whole path segments, of the same variable or
// temporary.
func TestWithin(t *testing.T) {
	tests := []struct {
		p, q Operand
		want bool
	}{
		{Operand{Var: 0, Temp: NoTemp, Path: "a.b"}, Operand{Var: 0, Temp: NoTemp, Path: "a"}, true},
		{Operand{Var: 0, Temp: NoTemp, Path: "a"}, Operand{Var: 0, Temp: NoTemp, Path: "a"}, true},
		{Operand{Var: 0, Temp: NoTemp, Path: "a"}, Operand{Var: 0, Temp: NoTemp}, true},
		{Operand{Var: 0, Temp: NoTemp, Path: "ab"}, Operand{Var: 0, Temp: NoTemp, Path: "a"}, false},
		{Operand{Var: 0, Temp: NoTemp}, Operand{Var: 0, Temp: NoTemp, Path: "a"}, false},
		{Operand{Var: 1, Temp: NoTemp, Path: "a"}, Operand{Var: 0, Temp: NoTemp, Path: "a"}, false},
		{Temp(0), Temp(1), false},
	}
	f := &Form{Vars: []*Var{NewVar(token.NoPos, "x", Typ[Int]), NewVar(token.NoPos, "y", Typ[Int])}, Temps: make([]TempInfo, 2)}
	for _, tt := range tests {
		if got := within(tt.p, tt.q); got != tt.want {
			t.Errorf("within(%s, %s) = %v, want %v", f.OperandString(tt.p), f.OperandString(tt.q), got, tt.want)
		}
	}
}

// TestPlaceKindOf walks paths from a variable's type: an element makes
// the place indexed, a deref of a borrow makes it borrowed, a deref of a
// pointer leaves it owned, and the fields alone name the place.
func TestPlaceKindOf(t *testing.T) {
	inner := &Struct{fields: []*Var{NewVar(token.NoPos, "b", Typ[Int])}}
	node := &Struct{fields: []*Var{NewVar(token.NoPos, "value", Typ[String]), NewVar(token.NoPos, "a", inner)}}
	tests := []struct {
		t      Type
		path   string
		kind   placeKind
		fields string
	}{
		{NewSlice(Typ[Int]), "[]", placeIndexed, ""},
		{NewRef(node, false), "*.value", placeBorrowed, "value"},
		{&Pointer{elem: node}, "*.value", placeOwned, "value"},
		{node, "a.b", placeOwned, "a.b"},
		{&Map{key: Typ[String], value: NewSlice(Typ[Int])}, "[].[]", placeIndexed, ""},
		{NewSlice(NewRef(Typ[Int], true)), "[].*", placeBorrowed, ""},
		{nil, "a", placeOwned, ""},
		{node, "", placeOwned, ""},
	}
	for _, tt := range tests {
		kind, fields := placeKindOf(tt.t, tt.path)
		if kind != tt.kind || fields != tt.fields {
			t.Errorf("placeKindOf(%v, %q) = %v, %q; want %v, %q", tt.t, tt.path, kind, fields, tt.kind, tt.fields)
		}
	}
}

// TestFieldPath keeps the fields of a path and leaves out its derefs
// and elements.
func TestFieldPath(t *testing.T) {
	tests := []struct{ path, want string }{
		{"*.a.[].b", "a.b"},
		{"a", "a"},
		{"[]", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := fieldPath(tt.path); got != tt.want {
			t.Errorf("fieldPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestFormMoves runs the move pass over the form beside the tree's on
// the programs whose paths the pass has rules for, and wants the two to
// agree, every disagreement being reported as an error of its own: a
// value moved in a loop's body is moved in the loop's next pass and
// after the loop, through a three-clause for, a range and a loop left
// by break; a move in a match guard is refused and is moved for the
// statements after the match, and not for the arms after it; a spawn
// moves its argument and what its closure captures where the closure
// stands, and a capture used after a spawned closure in the body is
// moved at its use; and a call's result indexed by a second call is
// evaluated before its index, so that the index is the use after the
// move.
func TestFormMoves(t *testing.T) {
	const decls = "func take(s string) int { return 0 }\nfunc pick(s string) []int { return []int{1} }\nfunc idx(s string) int { return 0 }\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(s string) { g := func() { spawn func() { print(1) }(); keep(s) }; g(); print(len(s)) }", []string{"use of moved value s; moved at 13:68"}},
		{"func f(s string) { n := pick(s)[idx(s)]; print(n) }", []string{"use of moved value s; moved at 13:30"}},
		{"func f(s string) { for i := 0; i < 2; i += 1 { keep(s) }; print(len(s)) }", []string{"s moved in a previous iteration of the loop", "use of moved value s"}},
		{"func f(s string, xs []int) { for _, x := range xs { keep(s); print(x) }; print(len(s)) }", []string{"s moved in a previous iteration of the loop", "use of moved value s"}},
		{"func f(s string) { for { keep(s); break }; print(len(s)) }", []string{"use of moved value s"}},
		{"func f(n int, s string) int { r := match n { 1 if take(s) > 0 => 1, _ => 2 }; return r + len(s) }", []string{"cannot move s in a match guard", "use of moved value s"}},
		{"func f(n int, s string) int { return match n { 1 if take(s) > 0 => 1, _ => len(s) } }", []string{"cannot move s in a match guard"}},
		{"func f(s string) { spawn func(v string) { print(v) }(s); print(len(s)) }", []string{"use of moved value s"}},
		{"func f(s string) { spawn func() { print(s) }(); print(len(s)) }", []string{"use of moved value s"}},
		{"func f(s string) { g := func() { print(len(s)); keep(s) }; g(); print(len(s)) }", []string{"use of moved value s; moved at 13:54"}},
		{"func f() { s := \"a\".Clone(); keep(s); g := func() { print(len(s)); s = \"b\".Clone() }; g() }", []string{"use of moved value s; moved at 13:35"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
