// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestMatchConditions checks a match with no value: each arm a
// condition, tried in turn, the arm _ required and last; and that a
// guard, which runs whether or not its arm is taken, moves nothing.
func TestMatchConditions(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(x int) string { return match { x > 0 => \"pos\", x < 0 => \"neg\", _ => \"zero\" } }", nil},
		{"func f(x int) int { n := 0; match { x > 0 => n = 1, _ => {} }; return n }", nil}, // a statement, _ doing nothing
		{"func f(xs []int) { for _, x := range xs { match { x < 0 => continue, x == 0 => break, _ => print(x) } } }", nil},
		{"func f(x int) int { return match { x > 0 => 1, x < 0 => 2 } }", []string{"a match with no value needs a last arm _ => ..., for when no condition holds"}},
		{"func f(x int) int { return match { _ => 1, x < 0 => 2 } }", []string{"this arm is never reached: the arm _ before it is taken whenever the ones above fail; _ comes last"}},
		{"func f(x int) int { return match { x => 1, _ => 2 } }", []string{"cannot use x (int) as bool in condition"}},
		{"func f(x int) int { return match { x > 0 => 1, _ => \"a\" } }", []string{"match branches have different types: int and string"}},
		// The arms are a branch set, as an if and else if chain is.
		{"func f(c bool) { s := \"a\"; match { c => keep(s), _ => {} }; keep(s) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; match { c => keep(s), _ => keep(s) } }", nil},
		{"func f(c bool) { s := \"a\"; keep(s); match { c => s = \"b\", _ => s = \"c\" }; keep(s) }", nil},
		{"func f(c bool) { s := \"a\"; keep(s); match { c => s = \"b\", _ => {} }; keep(s) }", []string{"use of moved value s"}}, // one arm gives it back, the other does not
		// A condition or a guard moves nothing: it runs even when its arm is not taken.
		{"func f() { s := \"a\"; match { len(s) > 9 => keep(s), _ => keep(s) } }", nil}, // len reads
		{"func f() { s := \"a\"; match { eat(s) => {}, _ => keep(s) } }", []string{"cannot move s in the condition of a match arm: it runs whether or not its arm is taken; lend it with ref"}},
		{"func f(o string?) { match o { Some(v) if eat(v) => {}, _ => {} } }", []string{"cannot move v in a match guard: it runs whether or not its arm is taken; lend it with ref"}},
		{"func f(o string?) { match o { Some(v) if read(v) > 0 => keep(v), _ => {} } }", nil}, // a guard lends
		// A name the guard declares is its own to move: it does not outlive the guard.
		{"func f() { match { (func() bool { t := \"x\"; keep(t); return true })() => {}, _ => {} } }", nil},
		{"func f(c bool) { match { (if c { t := \"x\"; keep(t); true } else { false }) => {}, _ => {} } }", nil},
		{"func f() { match { (match Some(\"x\".Clone()) { Some(v) => eat(v), None => false }) => {}, _ => {} } }", nil}, // the inner match's v is made and moved inside the condition
		{"func f(o string?) { match o { Some(v) if (func() bool { t := \"x\"; keep(t); return true })() => keep(v), _ => {} } }", nil},
		{"func f(o string?) { match { (match o { Some(v) => eat(v), None => false }) => {}, _ => {} } }", []string{"cannot move o in the condition of a match arm"}}, // the inner match takes o, declared outside
		{"func f(p Pair) { match { eat(p.a) => {}, _ => {} } }", []string{"cannot move p.a in the condition of a match arm"}},
		{"func f(o string?, s string) { match { (o ?? s).Len() > 0 => {}, _ => {} } }", []string{"cannot move o", "cannot move s"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+"func eat(s string) bool { return len(s) > 9 }\nstruct Pair { a string; b string }\n"+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
