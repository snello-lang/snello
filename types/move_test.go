// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestMoves checks the move pass rule by rule on small programs: what
// moves, what lends, what a branch or a loop does to a move, and what
// an assignment restores. The negative programs under examples/core
// cover the messages; these cover the edges between them.
func TestMoves(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// What moves and what does not.
		{"func f() { s := \"a\"; t := s; keep(t); keep(s) }", []string{"use of moved value s; moved at"}},
		{"func f() { x := 1; y := x; print(x, y) }", nil},                  // Copy
		{"func f() { s := \"a\"; read(s); keep(s) }", nil},                 // a ref parameter lends
		{"func f() { s := \"a\"; fmt.Println(s); keep(s) }", nil},          // any lends
		{"func f() { s := \"a\"; t := s + \"b\"; keep(s); keep(t) }", nil}, // operators read
		{"func f() { s := \"a\"; t := \"b\"; t += s; keep(s) }", nil},      // so does +=
		{"func f() { s := \"a\"; p := &s; keep(s); print(p) }", []string{"use of moved value s"}},
		{"func f() { s := \"a\"; xs := []string{s}; keep(s); print(xs) }", []string{"use of moved value s"}},
		{"func f() { s := \"a\"; pair := (s, 1); keep(s); print(pair) }", []string{"use of moved value s"}},
		{"func f() { s := \"a\"; n := Node{value: s, next: None, n: 0}; keep(s); print(n) }", []string{"use of moved value s"}},
		{"func f() { s := \"a\"; defer keep(s); keep(s) }", []string{"use of moved value s"}},
		{"func f() string? { s := \"a\"; o := Some(s); keep(s); return o }", []string{"use of moved value s"}},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; v := take(mut n.value); keep(v); print(n) }", nil},
		// A part of a value no name holds stays where it is: the whole is dropped with the statement.
		{"func pick(ps mut []Pair) mut Pair { return mut ps[0] }\nfunc f(ps mut []Pair) { s := pick(mut ps).a; keep(s) }", []string{"cannot move pick(...).a out of a borrow"}},
		{"func pick(ps mut []Pair) mut Pair { return mut ps[0] }\nfunc f(ps mut []Pair) { s := pick(mut ps).a.Clone(); keep(s) }", nil}, // cloned, not moved
		{"func pick(ns mut []Node) mut Node { return mut ns[0] }\nfunc f(ns mut []Node) { n := pick(mut ns).n; print(n) }", nil},        // a Copy part is read
		{"func mkp() *Node { return &mk(\"a\") }\nfunc f() { s := (*mkp()).value; keep(s) }", []string{"cannot move (*mkp()).value out of a temporary"}},
		{"func mkp() *Node { return &mk(\"a\") }\nfunc f() { n := *mkp(); keep(n.value) }", []string{"cannot move *mkp() out of a temporary"}},
		{"func mkp() *Node { return &mk(\"a\") }\nfunc f() { p := mkp(); n := *p; keep(n.value) }", nil}, // bound first
		{"func mkp() *Node { return &mk(\"a\") }\nfunc f() *Node { return (*mkp()).next ?? return &mk(\"b\") }", []string{"cannot move (*mkp()).next out of a temporary"}},
		// Assignment restores the name.
		{"func f() { s := \"a\"; keep(s); s = \"b\"; keep(s) }", nil},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; v := n.value; n.value = \"b\"; keep(v); print(n) }", nil},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; v := n.value; print(n.n); keep(v); keep(n.value) }", []string{"use of moved value n.value"}},
		// Branches join, and a branch that leaves does not count.
		{"func f(c bool) { s := \"a\"; if c { keep(s) } else { keep(s) }; keep(s) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; if c { keep(s); return }; keep(s) }", nil},
		{"func f(c bool) { s := \"a\"; if c { keep(s) } else { return }; keep(s) }", []string{"use of moved value s"}},

		// A value given back after a move: owned where every path gave it back, moved where one did not.
		{"func f(c bool) { s := \"a\"; keep(s); if c { s = \"b\" }; keep(s) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; keep(s); if c { s = \"b\" } else { s = \"c\" }; keep(s) }", nil},
		{"func f(c bool) { s := \"a\"; keep(s); if c { s = \"b\"; keep(s) } else { s = \"c\" }; keep(s) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; keep(s); if c { s = \"b\"; keep(s); s = \"d\" } else { s = \"c\" }; keep(s) }", nil},
		{"func f(o string?) string? { match o { Some(v) => keep(v), None => o = Some(\"a\") }; return o }", []string{"use of moved value o"}},
		{"func f(o string?) string? { match o { Some(v) => o = Some(v), None => o = Some(\"a\") }; return o }", nil},
		{"func f(c bool) { s := \"a\"; keep(s); for c { s = \"b\" }; keep(s) }", []string{"use of moved value s"}},       // the body may not run
		{"func f(c bool) { s := \"a\"; keep(s); for c { s = \"b\"; keep(s); s = \"c\" } }", nil},                         // owned again within the iteration
		{"func f(c bool, d bool) { s := \"a\"; if c { keep(s); if d { s = \"b\" } else { s = \"c\" } }; keep(s) }", nil}, // a move inside a branch, given back by a nested branch set
		{"func f(c bool, d bool) { s := \"a\"; if c { keep(s); if d { s = \"b\" } }; keep(s) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; keep(s); switch { case c: s = \"b\"; default: s = \"c\" }; keep(s) }", nil}, // a switch is a region and a branch set
		{"func f(c bool) { s := \"a\"; keep(s); switch { case c: s = \"b\" }; keep(s) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; keep(s); for { s = \"b\"; break }; keep(s) }", nil},                                                          // a loop with no condition runs its body
		{"func f(c bool) { s := \"a\"; keep(s); for { if c { break }; s = \"b\"; break }; keep(s) }", []string{"use of moved value s"}},             // but one exit gave nothing back
		{"func f(c bool) { p := Pair{a: \"x\", b: \"y\"}; keep(p.a); if c { p.a = \"1\" } else { p.a = \"2\" }; keepPair(p) }", nil},                // a part given back on every path clears the partial move
		{"func f(c bool) { p := Pair{a: \"x\", b: \"y\"}; keep(p.a); if c { p.a = \"1\" } else { p = Pair{a: \"1\", b: \"2\"} }; keep(p.a) }", nil}, // the whole on one path counts for the part
		{"func f(o string?) string? { match o { None => o = Some(\"a\"), Some(v) => o = Some(v) }; return o }", nil},                                // the arm order does not matter
		{"func f(o string?) string? { match o { None => {}, Some(v) => o = Some(v) }; return o }", []string{"use of moved value o"}},
		{"func f(c bool) { s := \"a\"; keep(s); g := func() { s = \"b\" }; if c { g() }; keep(s) }", []string{"use of moved value s"}}, // a closure may never run
		{"func f(o string?) string { s := \"a\"; t := o ?? return s; keep(s); return t }", nil},
		{"func f(o string?) string { s := \"a\"; t := o ?? s; keep(s); return t }", []string{"use of moved value s"}},
		{"func f(st State) { s := \"a\"; match st { Closed => keep(s), Open(_) => {}, Failed(_) => {} }; keep(s) }", []string{"use of moved value s"}},
		{"func f(st State) { s := \"a\"; match st { Closed => { keep(s); return }, _ => {} }; keep(s) }", nil},
		{"func f(n int) { s := \"a\"; switch n { case 1: keep(s) }; keep(s) }", []string{"use of moved value s"}},
		// Loops.
		{"func f() { s := \"a\"; for { keep(s); break } }", nil},
		{"func f() { s := \"a\"; for { switch { default: keep(s); break } } }", nil},                                                                   // a break in a switch leaves the loop
		{"func f(ch chan int) { s := \"a\"; for { select { case v := <-ch: keep(s); break; default: break } } }", nil},                                 // and in a select
		{"func f() { s := \"a\"; for { match Some(1) { Some(n) => { keep(s); break }, None => {} } } }", nil},                                          // and in a match
		{"func f(c bool) { s := \"a\"; for { switch { case c: keep(s); default: break } } }", []string{"s moved in a previous iteration of the loop"}}, // the case that does not break comes round
		{"func f() { s := \"a\"; for { switch { default: keep(s); break } }; keep(s) }", []string{"use of moved value s"}},                             // the break is the loop's one exit, and s left on it
		{"func f(ch chan int) { s := \"a\"; for { select { case v := <-ch: keep(s); break } }; keep(s) }", []string{"use of moved value s"}},
		{"func f() { break }", []string{"break is not inside a loop"}},
		{"func f(c bool) { switch { case c: break } }", []string{"break is not inside a loop"}}, // a switch is not a loop
		{"func f(c bool) { switch { case c: continue } }", []string{"continue is not inside a loop"}},
		{"func f() { for { g := func() { break } } }", []string{"break is not inside a loop"}}, // a closure is a function of its own
		{"func f(c bool) { s := \"a\"; for { if c { keep(s); continue }; break } }", []string{"s moved in a previous iteration of the loop"}},
		{"func f() { for i := 0; i < 2; i += 1 { s := \"a\"; keep(s) } }", nil}, // declared inside
		{"func f() { s := \"a\"; for i := 0; i < 2; i += 1 { keep(s); s = \"b\" } }", nil},
		{"func f(xs []string) { for _, x := range xs { keep(x) }; print(xs) }", []string{"use of moved value xs"}},
		{"func f(xs []string) { for _, x := range ref xs { read(x) }; print(xs) }", nil},
		// Taking apart.
		{"func f(o string?) { if s := o { keep(s) }; print(o) }", []string{"use of moved value o"}},
		{"func f(o int?) { if n := o { print(n) }; print(o) }", nil},
		{"func f(st State) { match st { Failed(r) => keep(r), _ => {} }; print(st) }", []string{"use of moved value st"}},
		{"func f(st State) { match st { Failed(_) => {}, _ => {} }; print(st) }", nil},
		{"func f(st ref State) string { return match st { Failed(r) => r.Clone(), _ => \"\" } }", nil},
		{"func f(p (string, int)) { s, n := p; keep(s); print(n, p) }", []string{"use of moved value p"}},
		// Flags for the base of a selector and for an assignment target reach no further than the place.
		{"func f() { s := \"a\"; keep(s); print(mk(s).n) }", []string{"use of moved value s"}},
		{"func f(xs []int) { s := \"a\"; keep(s); xs[len(s)] = 1 }", []string{"use of moved value s"}},
		{"func f(p mut string) { q := p; *p = \"b\"; print(q) }", []string{"use of moved value p"}},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; keep(n.value); n.n = 1 }", nil},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; m := n; n.n = 1; print(m) }", []string{"use of moved value n"}},
		// A scrutinee consumed by a later arm is gone after every arm.
		{"func f(st State) { s := \"a\"; match st { Closed => keep(s), Failed(r) => keep(r), _ => {} }; keep(s) }", []string{"use of moved value s"}},
		{"func f(st State) { match st { Closed => {}, Failed(r) => keep(r), _ => {} }; print(st) }", []string{"use of moved value st"}},
		// A branch's value leaves with the expression.
		{"func f(c bool) { s := \"a\"; t := match c { true => s, false => \"b\" }; keep(s); keep(t) }", []string{"use of moved value s"}},
		{"func f(c bool) { s := \"a\"; t := if c { s } else { \"b\" }; keep(s); keep(t) }", []string{"use of moved value s"}},
		{"func f(st State) string { return match st { Failed(r) => r, _ => \"\" } }", nil},
		// A closure sees what moved before it.
		{"func f() { s := \"a\"; keep(s); g := func() int { return len(s) }; g() }", []string{"use of moved value s"}},
		// Closures and receivers.
		{"func f() { s := \"a\"; g := func() int { return len(s) }; keep(s); g() }", []string{"cannot move s: s is borrowed by g"}}, // the closure holds s
		{"func f() { s := \"a\"; g := func() { keep(s) }; keep(s); g() }", []string{"use of moved value s"}},
		{"func f(n Node) { n.Consume(); print(n) }", []string{"use of moved value n"}},
		{"func f(n Node) { n.Peek(); print(n) }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+"func (n Node) Consume() {}\nfunc (n ref Node) Peek() {}\nfunc mk(s string) Node { return Node{value: s, next: None, n: 0} }\nstruct Pair { a string; b string }\nfunc keepPair(p Pair) {}\n"+tt.src)
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
