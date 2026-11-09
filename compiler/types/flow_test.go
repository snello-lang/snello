// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestFlowPaths checks that a borrow is live along the paths a function
// can take, not by the order of its lines: each kind of edge the graph
// has, a branch, a loop's next pass, a break, a continue, a for loop's
// post statement, a closure's body, and the end of a block, which drops
// what it declares.
func TestFlowPaths(t *testing.T) {
	const helpers = "struct Holder { src ref string; n int }\nfunc fill(h mut Holder) {}\nfunc cond() bool { return true }\nfunc step(r ref []int) int { return 1 }\n"
	tests := []struct {
		src  string
		want []string
	}{
		// A local dies at the end of its block; a borrow of it held outside may not be used after.
		{"func f() { s := \"a\"; r := ref s; { t := \"b\".Clone(); r = ref t }; print(r) }", []string{"cannot keep a borrow of t in r: t is dropped at the end of its block at 14:64, used at 14:73"}},
		{"func f() { s := \"a\"; r := ref s; { t := \"b\".Clone(); r = ref t; print(r) } }", nil},
		{"func f() { s := \"a\"; r := ref s; { t := \"b\".Clone(); r = ref t; print(r) }; r = ref s; print(r) }", nil}, // given another borrow before its next use
		{"func f() { s := \"a\"; r := ref s; if cond() { t := \"b\".Clone(); r = ref t }; print(r) }", []string{"cannot keep a borrow of t in r: t is dropped at the end of its block"}},
		{"func f() { s := \"a\"; r := ref s; match cond() { true => { t := \"b\".Clone(); r = ref t }, false => {} }; print(r) }", []string{"cannot keep a borrow of t in r: t is dropped at the end of its block"}},
		{"func f() { s := \"a\"; r := ref s; if t := Some(\"b\".Clone()) { r = ref t }; print(r) }", []string{"cannot keep a borrow of t in r: t is dropped at the end of its block"}}, // an if binding is the body's
		// A field holds what is stored into it, and gives it up when given another value.
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h.src = ref t }; print(h.src) }", []string{"cannot keep a borrow of t in h: t is dropped at the end of its block"}},
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h.src = ref t; h.src = ref s }; print(h.src) }", nil},
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h.src = ref t; h = Holder{src: ref s, n: 1} }; print(h.src) }", nil},        // the whole given a value lets go of every field's
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h.src = ref t }; print(h.n) }", []string{"cannot keep a borrow of t in h"}}, // h is one value, whatever field is read
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h = Holder{src: ref t, n: 1} }; print(h.n) }", []string{"cannot keep a borrow of t in h"}},
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; s = \"c\"; print(h.n) }", []string{"cannot assign to s: s is borrowed by h"}},
		// A mut parameter is the caller's: a signature cannot say it now borrows anything but itself.
		{"func g(h mut Holder) { t := \"b\".Clone(); h.src = ref t }", []string{"cannot store a borrow of t in h: h is the caller's, and a signature cannot say that it borrows t"}},
		{"func g(h mut Holder, s ref string) { h.src = s }", []string{"cannot store a borrow of s in h: h is the caller's"}},
		{"func g(h mut Holder) { h.src = h.src }", nil}, // what it held already
		{"func g(h mut Holder) { h.n = 2 }", nil},
		// Branches are exact: an access collides only with what is live on its own path.
		{"func f() { s := \"a\".Clone(); r := ref s; if cond() { s = \"b\".Clone() } else { print(r) } }", nil},
		{"func f() { s := \"a\".Clone(); r := ref s; if cond() { s = \"b\".Clone() }; print(r) }", []string{"cannot assign to s: s is borrowed by r at 14:30, used at 14:79"}},
		{"func f() { s := \"a\".Clone(); r := ref s; match cond() { true => keep(s), false => print(r) } }", nil},
		{"func f(o string?) { s := \"a\".Clone(); r := ref s; x := o ?? { keep(s); return }; print(r); print(x) }", nil}, // the side that leaves never reaches the use
		// A borrow given another value lets go of the old one; a branch that does not give it one keeps it.
		{"func f() { s := \"a\".Clone(); z := \"z\"; r := ref s; r = ref z; s = \"b\".Clone(); print(r) }", nil},
		{"func f() { s := \"a\".Clone(); z := \"z\"; r := ref s; if cond() { r = ref z }; s = \"b\".Clone(); print(r) }", []string{"cannot assign to s: s is borrowed by r"}},
		{"func f() { s := \"a\".Clone(); z := \"z\"; r := ref s; if cond() { r = ref z } else { r = ref z }; s = \"b\".Clone(); print(r) }", nil},
		// A copy holds what its source held: a mut borrow moved on, a closure copied.
		{"func f() { n := 0; a := mut n; b := a; print(n); *b += 1 }", []string{"cannot use n: n is borrowed as mut by b"}},
		{"func f() { xs := []int{1}; g := func() { xs.Push(1) }; h := g; r := ref xs; h(); print(r) }", []string{"a closure that borrows cannot be copied to another variable"}},
		// An assignment's store comes after its value: the value may read the borrow the store ends.
		{"func f() { s := \"a\".Clone(); r := ref s; s = (*r).Clone(); print(s) }", nil},
		{"func f() { s := \"a\".Clone(); r := ref s; s = \"b\".Clone(); print(r) }", []string{"cannot assign to s: s is borrowed by r"}},
		// A loop's next pass reaches what its body reads before the borrow is made.
		{"func f() { s := \"a\"; r := ref s; for i := 0; i < 2; i += 1 { print(r); t := \"b\".Clone(); r = ref t } }", []string{"cannot keep a borrow of t in r: t is dropped at the end of its block at 14:100, used in the next pass of the loop at 14:68"}},
		{"func f() { s := \"a\"; r := ref s; for i := 0; i < 2; i += 1 { t := \"b\".Clone(); r = ref t; print(r) } }", nil},
		{"func f() { s := \"a\"; r := ref s; for i := 0; i < 2; i += 1 { r = ref s; print(r); t := \"b\".Clone(); r = ref t } }", nil}, // given another borrow before the read, on every pass
		{"func f() { s := \"a\"; r := ref s; for i := 0; i < 2; i += 1 { t := \"b\".Clone(); r = ref t; if i == 1 { break } }; print(r) }", []string{"cannot keep a borrow of t in r: t is dropped where its block is left"}},
		{"func f() { s := \"a\"; r := ref s; for i := 0; i < 2; i += 1 { t := \"b\".Clone(); r = ref t; continue }; print(r) }", []string{"cannot keep a borrow of t in r"}},
		{"func f() { s := \"a\"; r := ref s; for { t := \"b\".Clone(); r = ref t; break }; print(r) }", []string{"cannot keep a borrow of t in r: t is dropped where its block is left"}},
		{"func f(xs []string) { s := \"a\"; r := ref s; for _, x := range ref xs { t := x.Clone(); r = ref t }; print(r) }", []string{"cannot keep a borrow of t in r"}},
		{"func f(xs []string) { s := \"a\"; r := ref s; for _, x := range xs { r = ref x }; print(r) }", []string{"cannot keep a borrow of x in r: x is dropped at the end of the loop's pass"}}, // a range loop's variables are each pass's own
		{"func f() { xs := []int{1}; r := ref xs; for i := 0; i < 2; i += step(r) { xs.Push(1) } }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r"}},                            // the post statement runs after the body
		{"func f() { xs := []int{1}; for i := 0; i < 2; i += 1 { xs.Push(1); r := ref xs; print(r) } }", nil},
		// A closure's body is a graph of its own; a variable from outside it may be used after it.
		{"func f() { s := \"a\"; r := ref s; set := func() { t := \"b\".Clone(); r = ref t }; set(); print(r) }", []string{"cannot store a borrow in r from inside a closure: r is declared outside it"}},
		{"func f() { s := \"a\"; r := ref s; show := func() { t := \"b\".Clone(); q := ref t; print(q) }; show(); print(r) }", nil},
		{"func f() { xs := []int{1}; r := ref xs; g := func() { xs.Push(1) }; g(); print(r) }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r"}}, // made here, the closure borrows what it captures
		// A returned borrow is followed on every path to the return.
		{"func g(s ref string) ref string { r := s; if cond() { z := \"z\"; r = ref z; return r }; return r }", []string{"cannot return a borrow of local z"}},
		{"func g(s ref string, t ref string) ref string { return s }", []string{"returns a borrow but has two borrowed parameters"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+helpers+tt.src)
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

// TestFlowModel checks that the graph follows how a program runs where
// the order of its text does not: a copy of a borrow, a guard that
// fails, a match that consumes its value, a closure's body, a range
// loop's elements and its passes, a deferred call, a Drop, a block's
// value, and a store through a borrow.
func TestFlowModel(t *testing.T) {
	const helpers = "struct Holder { src ref string; n int }\nstruct Peek impl Drop { src ref string }\nfunc (p mut Peek) Drop() { print(p.src) }\nfunc cond() bool { return true }\nfunc drain(xs []string) {}\nstruct Count { n int; name string }\nfunc bump(c mut Count) { c.n += 1 }\nfunc keep2(c Count) {}\nfunc show(h mut Holder) {}\nfunc peek(x ref int) {}\nfunc apply(g func()) { g() }\nstruct Wrap { in Holder }\nstruct Point { h mut Holder }\nstruct Server { n int; name string }\nfunc (s mut Server) cleanup() { s.n = 0 }\nfunc keep3(s Server) {}\nfunc get() int? { return Some(1) }\n"
	tests := []struct {
		src  string
		want []string
	}{
		// A copy of a ref borrow holds what it held: the source may be given another without freeing it.
		{"func g(p ref string) ref string { z := \"z\".Clone(); r := ref z; q := r; r = p; print(r); return q }", []string{"cannot return a borrow of local z"}},
		{"func f() { s := \"a\".Clone(); z := \"z\".Clone(); r := ref s; q := r; r = ref z; keep(s); print(q, r) }", []string{"cannot move s: s is borrowed by q"}},
		{"func f() { s := \"a\"; r := ref s; { h := Holder{src: ref s, n: 0}; r = h.src }; print(r) }", nil}, // r holds s, which outlives h
		{"func f() { s := \"a\".Clone(); r := ref s; q := r; r = ref s; s = \"b\".Clone(); print(r) }", []string{"cannot assign to s: s is borrowed by r"}},
		// A borrow read through another keeps what it points to lent, not the variable: the variable may point elsewhere.
		{"func f(m mut map[string]string, n mut map[string]string) { r := ref m; x := r.Get(\"a\"); r = ref n; print(x) }", nil},
		{"func f(m mut map[string]string, n mut map[string]string) { r := ref m; x := r.Get(\"a\"); r = ref n; m.Remove(\"a\"); print(x) }", []string{"cannot borrow m as mut: m is borrowed as ref by x"}},
		{"func f(m mut map[string]string) { r := mut m; x := r.Get(\"a\"); r.Remove(\"a\"); print(x) }", []string{"cannot borrow r as mut: r is borrowed as ref by x"}},
		// A guard that fails goes on to the next arm, with what it did.
		{"func f() { s := \"a\".Clone(); z := \"z\".Clone(); r := ref s; n := 1; match n { 1 if (if n == 1 { r = ref z; false } else { false }) => print(1), _ => keep(z) }; print(r) }", []string{"cannot move z: z is borrowed by r"}},
		{"func f() { s := \"a\".Clone(); z := \"z\".Clone(); r := ref s; n := 1; match { (if n == 1 { r = ref z; false } else { false }) => print(1), n > 0 => keep(z), _ => {} }; print(r) }", []string{"cannot move z: z is borrowed by r"}},
		// A match that binds a part by value consumes its value before any arm.
		{"func f() { o := Some(\"p\".Clone()); r := ref o; n := 1; match o { Some(x) if n == 0 => keep(x), Some(y) => { keep(y); print(r) }, None => {} } }", []string{"cannot move o: o is borrowed by r"}},
		// A value carried out of a block or an arm holds what the block's names held.
		{"func f() { s := \"a\"; x := if cond() { t := \"b\".Clone(); ref t } else { ref s }; print(x) }", []string{"cannot keep a borrow of t: t is dropped at the end of its block"}},
		{"func f() { s := \"a\"; x := match cond() { true => { t := \"b\".Clone(); ref t }, false => ref s }; print(x) }", []string{"cannot keep a borrow of t: t is dropped at the end of its block"}},
		{"func f(m mut map[string]string) { x := match m.Get(\"a\") { Some(v) => v, None => return }; m.Remove(\"a\"); print(x) }", []string{"cannot borrow m as mut: m is borrowed as ref by x"}},
		{"func f() { s := \"a\"; x := if cond() { r := ref s; r } else { ref s }; print(x) }", nil},
		// A closure's body may not store a borrow in a variable from outside it, nor through one.
		{"func f() { s := \"a\"; z := \"z\"; r := ref s; set := func() { r = ref z }; set(); print(r) }", []string{"cannot store a borrow in r from inside a closure"}},
		{"func f() { s := \"a\"; r := ref s; outer := func() { t := \"b\".Clone(); inner := func() { r = ref t }; inner() }; outer(); print(r) }", []string{"cannot store a borrow in r from inside a closure"}},
		{"func f() { s := \"a\"; r := ref s; show := func() { q := r; print(q) }; show() }", nil},
		// A closure reads a borrow variable it captures when it is called: the variable may not point elsewhere meanwhile.
		{"func f() { s := \"a\"; r := ref s; g := func() { print(r) }; { t := \"b\".Clone(); r = ref t }; g() }", []string{"cannot assign to r: r is borrowed by g"}},
		// A closure assigned with = holds what it captures, as one bound with := does.
		{"func f() { g := func() {}; { t := \"b\".Clone(); g = func() { print(t) } }; g() }", []string{"a closure that borrows cannot be assigned with ="}},
		{"func f() { s := \"a\".Clone(); g := func() {}; g = func() { print(s) }; keep(s); g() }", []string{"a closure that borrows cannot be assigned with ="}},
		// A range loop's borrowed elements keep the collection lent, past a break; a loop over nothing runs none of its body.
		{"func f(xs []string) { for _, x := range ref xs { drain(xs); print(x); break } }", []string{"cannot move xs: xs is borrowed by x"}},
		{"func f(xs mut []int) { for _, x := range xs { xs.Clear(); print(*x); break } }", []string{"cannot borrow xs as mut: xs is already borrowed as mut by x"}},
		{"func f(xs []string) { s := \"a\".Clone(); z := \"z\"; r := ref s; keep(s); for _, x := range ref xs { r = ref z }; print(r) }", []string{"cannot move s: s is borrowed by r"}},
		// A store through a mut borrow is a store into what it points to, and lets go of what it held, when the borrow has one place it may point to.
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); m := mut h; m.src = ref t }; print(h.src) }", []string{"cannot keep a borrow of t in h: t is dropped at the end of its block"}},
		{"func f() { s := \"a\"; z := \"z\"; h := Holder{src: ref s, n: 0}; m := mut h; m.src = ref z; print(h.src) }", nil},
		{"func f() { s := \"a\"; z := \"z\"; h := Holder{src: ref s, n: 0}; m := mut h; { t := \"b\".Clone(); m.src = ref t; m.src = ref z }; print(h.src) }", nil},
		{"func f() { s := \"a\"; z := \"z\"; h := Holder{src: ref s, n: 0}; m := mut h; { t := \"b\".Clone(); m.src = ref t; m.src = ref z; print(m.src) }; print(h.src) }", nil},
		{"func f() { s := \"a\"; z := \"z\"; h := Holder{src: ref s, n: 0}; h2 := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h.src = ref t; m := mut h; if cond() { m = mut h2 }; m.src = ref z }; print(h.src) }", []string{"cannot keep a borrow of t in h"}}, // m may point to h2: h keeps what it held
		// A struct copied to another variable keeps each borrow in the field that holds it: the field may be given another.
		{"func f() { s := \"a\"; g := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h := Holder{src: ref t, n: 0}; g = h; g.src = ref s }; print(g.src) }", nil},
		{"func f() { s := \"a\"; g := Holder{src: ref s, n: 0}; { t := \"b\".Clone(); h := Holder{src: ref t, n: 0}; g = h; g.n = 1 }; print(g.src) }", []string{"cannot keep a borrow of t in g"}},
		// A closure may give a variable holding a borrow a value that carries none.
		{"func f() { s := \"a\"; cur := Some(ref s); clear := func() { cur = None }; clear(); print(cur) }", nil},
		{"func f() { s := \"a\"; cur := Some(ref s); set := func() { z := \"z\".Clone(); cur = Some(ref z) }; set(); print(cur) }", []string{"cannot store a borrow in cur from inside a closure"}},
		// A mut parameter is the caller's: the store cannot be seen by the caller, so it is refused where it is written.
		{"func g(h mut Holder, t ref string) { h.src = t }", []string{"cannot store a borrow of t in h: h is the caller's"}},
		// A deferred call holds what it borrows until the function returns.
		{"func f() { { t := \"b\".Clone(); defer read(ref t) } }", []string{"cannot keep a borrow of t in the deferred call: t is dropped at the end of its block", "and the deferred call runs when the function returns"}},
		{"func f() { s := \"a\".Clone(); defer read(s); keep(s) }", []string{"cannot move s: s is borrowed by the deferred call"}},
		{"func f() { s := \"a\".Clone(); defer func() { print(s) }(); keep(s) }", []string{"cannot move s: s is borrowed by the deferred call"}},
		{"func f() { s := \"a\".Clone(); defer read(ref s) }", nil},
		{"func f() int { c := Count{n: 1, name: \"c\"}; defer bump(mut c); c.n += 1; s := ref c; return s.n }", nil}, // a whole variable is reached only when the call runs
		{"func f() { c := Count{n: 1, name: \"c\"}; defer bump(mut c); keep2(c) }", []string{"cannot move c: c is borrowed by the deferred call"}},
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 1}; defer show(mut h); h.n = 2 }", []string{"a deferred call borrows only a whole variable, one that is a borrow or holds none: mut h is not one"}}, // what h borrows would be read when the call runs
		{"func f() { xs := []int{1}; defer peek(ref xs[0]); grow(mut xs) }", []string{"a deferred call borrows only a whole variable, one that is a borrow or holds none: ref xs[0] is not one"}},               // an element may move before then
		// A deferred call on a borrow variable reads it when it runs: it may not be pointed elsewhere before, and what it points to is held as it would be by the variable.
		{"func (s mut Server) Run() { defer s.cleanup(); s.n = 1 }", nil},
		{"func f(a mut Server, b mut Server) { s := mut *a; defer s.cleanup(); s = mut *b }", []string{"cannot assign to s: s is borrowed by the deferred call"}},
		{"func f() { h := Server{n: 0, name: \"s\"}; r := mut h; defer r.cleanup(); r.n = 1; keep3(h) }", []string{"cannot move h: h is borrowed by the deferred call"}},
		{"func f() { h := Server{n: 0, name: \"s\"}; r := mut h; defer func() { r.cleanup() }(); r.n = 1 }", nil},
		{"func f() { s := \"a\"; r := ref s; { t := \"b\".Clone(); r = ref t; defer read(r) } }", []string{"cannot keep a borrow of t in the deferred call: t is dropped at the end of its block"}},
		// A closure that borrows is used where it is made: bound with :=, called, or passed to a call.
		{"func f() { s := \"a\"; g := if cond() { func() { print(s) } } else { func() {} }; g() }", []string{"a closure that borrows cannot be chosen by if or match, or come out of a block"}},
		{"func f() { s := \"a\".Clone(); g := func() { print(s) }; g(); keep(s) }", nil},
		{"func f(xs mut []int) { n := 0; apply(func() { n += 1 }) }", nil},
		{"func f() { s := \"a\".Clone(); r := ref s; g := func() { keep(s) }; g(); print(r) }", []string{"cannot move s: s is borrowed by r"}}, // a closure that moves what it captures moves it where it is made
		// A deferred call borrows only whole variables that hold no borrow.
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 1}; defer func() { print(h.n) }() }", []string{"a deferred call borrows only a whole variable, one that is a borrow or holds none: the closure captures h, which holds one"}},
		{"func f() { s := \"a\".Clone(); g := func() { print(s) }; defer g() }", []string{"a deferred call cannot call g: it is a closure that borrows"}},
		{"func f() { s := \"a\".Clone(); defer apply(func() { print(s) }) }", []string{"a deferred call borrows only a whole variable, one that is a borrow or holds none", "defer a closure that makes the call"}},
		{"func f() { s := \"a\"; g := match cond() { true => func() { print(s) }, false => func() {} }; g() }", []string{"a closure that borrows cannot be chosen by if or match, or come out of a block"}},
		{"func f() { c := Count{n: 1, name: \"c\"}; defer func() { print(c.n) }(); c.n = 2 }", nil},
		// Each arm of a match makes its own accesses.
		{"func f(st mut State) { r := ref st; match st { Failed(x) => { *x = \"n\".Clone(); print(r) }, _ => {} } }", []string{"cannot borrow st as mut: st is borrowed as ref by r"}},
		// A return, a panic or a call that never returns drops what every open block declares; replacing a value whose Drop reads a borrow runs that Drop.
		{"func f() { s := \"a\"; p := Peek{src: ref s}; t := \"b\".Clone(); p.src = ref t; return }", []string{"cannot keep a borrow of t in p", "used when p is dropped"}},
		{"func f() { s := \"a\"; p := Peek{src: ref s}; t := \"b\".Clone(); p.src = ref t; panic(\"x\") }", []string{"cannot keep a borrow of t in p", "used when p is dropped"}},
		{"func f() { s := \"a\"; p := Peek{src: ref s}; t := \"b\".Clone(); p.src = ref t; if cond() { panic(\"x\") }; p.src = ref s }", []string{"cannot keep a borrow of t in p", "used when p is dropped"}},
		{"func f() int? { s := \"a\"; p := Peek{src: ref s}; { t := \"b\".Clone(); p.src = ref t; n := get()?; p.src = ref s; print(n) }; return Some(0) }", []string{"cannot keep a borrow of t in p", "used when p is dropped"}},
		// A guard that fails drops what its pattern bound by value, on its way to the next arm.
		{"func f() { s := \"a\".Clone(); r := ref s; o := Some(\"p\".Clone()); match o { Some(v) if (if cond() { r = ref v; false } else { false }) => print(1), _ => print(r) } }", []string{"cannot keep a borrow of v in r: v is dropped at the end of its block"}},
		{"func f() { s := \"a\".Clone(); r := ref s; o := Some(\"p\".Clone()); match o { Some(v) if (if cond() { r = ref v; true } else { false }) => print(r), _ => print(1) } }", nil},
		// A select case and the right side of || are each their own path; a break leaves only the innermost loop.
		{"func f(ch chan string) { s := \"a\"; r := ref s; select {\ncase <-ch:\n t := \"b\".Clone(); r = ref t\ndefault:\n}\nprint(r) }", []string{"cannot keep a borrow of t in r: t is dropped at the end of its block"}},
		{"func f(c bool) { s := \"a\"; r := ref s; { t := \"b\".Clone(); r = ref t; ok := c || (if true { r = ref s; true } else { r = ref s; true }); print(ok) }; print(r) }", []string{"cannot keep a borrow of t in r"}},
		{"func f() { s := \"a\"; r := ref s; for i := 0; i < 2; i += 1 { print(r); for j := 0; j < 2; j += 1 { t := \"b\".Clone(); r = ref t; break } } }", []string{"cannot keep a borrow of t in r", "used in the next pass of the loop"}},
		{"func f() { s := \"a\"; p := Peek{src: ref s}; { t := \"b\".Clone(); p.src = ref t }; p = Peek{src: ref s} }", []string{"cannot keep a borrow of t in p"}},
		// && and || evaluate their right side only on one path.
		{"func f(c bool) { s := \"a\"; r := ref s; { t := \"b\".Clone(); r = ref t; ok := c && (if true { r = ref s; true } else { r = ref s; true }); print(ok) }; print(r) }", []string{"cannot keep a borrow of t in r"}},
		// A literal or a variant carried out of a block holds what it holds.
		{"func f() { s := \"a\"; h := if cond() { t := \"b\".Clone(); Holder{src: ref t, n: 1} } else { Holder{src: ref s, n: 0} }; print(h.src) }", []string{"cannot keep a borrow of t: t is dropped at the end of its block"}},
		// A store reaching the caller's places is followed through a copy, a literal and a field.
		{"func g(l mut Holder, name ref string) { m := l; m.src = name }", []string{"cannot store a borrow of name in l: l is the caller's"}},
		{"func g(l mut Wrap, name ref string) { l.in = Holder{src: name, n: 0} }", []string{"cannot store a borrow of name in l: l is the caller's"}},
		{"func g(w Point, name ref string) { w.h.src = name }", []string{"cannot store a borrow of name in w: w is the caller's"}},
		{"func g(w Point, name ref string) { local := Holder{src: name, n: 0}; w.h = mut local; w.h.src = name; print(w.h.n) }", nil}, // the field pointed at the function's own
		{"func g(l mut Holder) { l.src = l.src }", nil},
		{"func f() { s := \"a\"; h := Holder{src: ref s, n: 0}; w := Point{h: mut h}; { t := \"b\".Clone(); w.h.src = ref t }; print(h.src) }", []string{"cannot keep a borrow of t in h"}},
		// A Drop may read what its value borrows: the drop is a use, so what it borrows must outlive it.
		{"func f() { s := \"a\"; p := Peek{src: ref s}; { t := \"b\".Clone(); p.src = ref t } }", []string{"cannot keep a borrow of t in p: t is dropped at the end of its block", "used when p is dropped"}},
		{"func f() { s := \"a\"; p := Peek{src: ref s}; print(p.src) }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+helpers+tt.src)
		if len(got) != 1 && len(tt.want) > 0 || len(tt.want) == 0 && len(got) != 0 {
			t.Errorf("%s:\n  got %d errors %q\n  want one with %q", tt.src, len(got), got, tt.want)
			continue
		}

		for _, w := range tt.want {
			if !strings.Contains(got[0], w) {
				t.Errorf("%s:\n  got  %q\n  want %q in it", tt.src, got[0], w)
			}
		}
	}
}

// TestBitset checks the sets the two passes work in.
func TestBitset(t *testing.T) {
	s, u := make(bitset, 2), make(bitset, 2)
	s.set(3)
	s.set(70)
	if !s.has(3) || !s.has(70) || s.has(4) {
		t.Fatalf("set: got %v", s)
	}

	s.del(3)
	if s.has(3) {
		t.Fatalf("del: 3 still in %v", s)
	}

	u.set(5)
	if !u.union(s) || u.union(s) {
		t.Fatalf("union: should grow once, got %v", u)
	}

	var got []int
	u.each(func(i int) bool { got = append(got, i); return true })
	if len(got) != 2 || got[0] != 5 || got[1] != 70 {
		t.Fatalf("each: got %v, want [5 70]", got)
	}

	got = got[:0]
	u.each(func(i int) bool { got = append(got, i); return false })
	if len(got) != 1 {
		t.Fatalf("each stops when told: got %v", got)
	}
}

// TestPathWithin checks which parts of a variable an assignment to a
// part reaches.
func TestPathWithin(t *testing.T) {
	tests := []struct {
		p, q string
		want bool
	}{
		{"src", "src", true},
		{"src.x", "src", true},
		{"src", "", true}, // every part is inside the whole
		{"srcs", "src", false},
		{"src", "src.x", false},
		{"a.b.c", "a.b", true},
	}
	for _, tt := range tests {
		if got := pathWithin(tt.p, tt.q); got != tt.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", tt.p, tt.q, got, tt.want)
		}
	}
}
