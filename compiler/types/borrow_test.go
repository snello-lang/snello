// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestBorrows checks the borrow pass rule by rule on small programs:
// what may be borrowed beside what, how long a borrow lives, what the
// owner may do while it lends, and how a borrow walks down a chain.
// The negative programs under examples/core cover the messages; these
// cover the edges between them.
func TestBorrows(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// A borrow of a temporary dies with its statement, so it cannot be kept.
		{"func f() { t := \"  pad  \".Clone().Trim(); print(t) }", []string{"cannot keep a borrow of a temporary: \"  pad  \".Clone() is dropped when the statement ends"}},
		{"func f() { r := ref \"x\".Clone(); print(r) }", []string{"cannot keep a borrow of a temporary: \"x\".Clone() is dropped when the statement ends"}},
		{"func f() { w := \"a b\".Clone().Fields(); print(w) }", []string{"cannot keep a borrow of a temporary: \"a b\".Clone() is dropped when the statement ends"}},
		{"func f() { p := Pair{a: \"a\", b: \"b\"}; t := p.a.Clone().Trim(); print(t) }", []string{"cannot keep a borrow of a temporary: p.a.Clone() is dropped when the statement ends"}},
		{"func f() { s := \"x\"; t := s.Trim(); print(t) }", nil},
		{"func f() { t := \"  lit  \".Trim(); print(t) }", nil},          // a literal lives for the whole program
		{"func f() { s := \"x\"; n := s.Clone().Len(); print(n) }", nil}, // n borrows nothing
		{"func f() { for w := range \"a b\".Clone().Fields() { print(w) } }", nil},
		{"func f() { if (k, v) := \"k=v\".Clone().SplitOnce(\"=\") { print(k); print(v) } }", nil},
		{"func f() { print(\"  x \".Clone().Trim()) }", nil},

		// Any number of ref, or one mut, and a borrow lives to its last use.
		{"func f() { xs := []int{1}; r := ref xs; xs.Push(2); print(r) }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r"}},
		{"func f() { xs := []int{1}; r := ref xs; print(r); xs.Push(2) }", nil},
		{"func f() { xs := []int{1}; xs.Push(len(xs)) }", nil},            // the arguments go first
		{"func f() { xs := []int{1}; xs.Insert(xs.Pop() ?? 0, 2) }", nil}, // even one that changes the receiver
		{"func f() { n := 0; a := mut n; b := mut n; bump(a, b) }", []string{"cannot borrow n as mut: n is already borrowed as mut by a"}},
		{"func f() { n := 0; a := mut n; *a += 1; b := mut n; *b += 1 }", nil},
		{"func f() { n := 0; a := mut n; bump(a, a) }", []string{"cannot borrow a as mut: a is already borrowed as mut"}},
		{"func f() { n := 0; bump(mut n, mut n) }", []string{"cannot borrow n as mut: n is already borrowed as mut"}},
		{"func f() { n := 0; a := mut n; print(n); *a += 1 }", []string{"cannot use n: n is borrowed as mut by a"}},
		{"func f() { n := 0; a := mut n; r := ref n; *a += 1; print(r) }", []string{"cannot borrow n as ref: n is already borrowed as mut by a"}},
		{"func f() { s := \"a\"; a := ref s; b := ref s; read(a); read(b) }", nil},
		{"func f(s mut string) { read(s) }", nil}, // a mut passes on as a ref
		{"func f(xs mut []int) { a := mut xs[0]; b := mut xs[1]; *a = 1; *b = 2 }", []string{"cannot borrow xs[1] as mut: xs is already borrowed as mut by a"}},
		{"func f(xs mut []int) { a := mut xs[0]; *a = 1; b := mut xs[1]; *b = 2 }", nil},
		{"func f(p mut Pair) { a := mut p.a; b := mut p.b; *a = \"x\"; *b = \"y\" }", nil}, // two fields
		{"func f(p mut Pair) { a := mut p.a; b := mut p.a; *a = \"x\"; *b = \"y\" }", []string{"cannot borrow p.a as mut: p.a is already borrowed as mut by a"}},
		// The owner is frozen while it lends.
		{"func f() { s := \"a\"; r := ref s; s = \"b\"; read(r) }", []string{"cannot assign to s: s is borrowed by r"}},
		{"func f() { s := \"a\"; r := ref s; keep(s); read(r) }", []string{"cannot move s: s is borrowed by r"}},
		{"func f() { x := 1; r := ref x; x += 1; print(r) }", []string{"cannot assign to x: x is borrowed by r"}},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; r := ref n.value; n.n = 1; read(r) }", nil},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; r := ref n.value; n.value = \"b\"; read(r) }", []string{"cannot assign to n.value: n.value is borrowed by r"}},
		{"func f() { n := Node{value: \"a\", next: None, n: 0}; r := ref n.value; n = Node{value: \"b\", next: None, n: 0}; read(r) }", []string{"cannot assign to n: n.value is borrowed by r"}},
		// Loops: a borrow used earlier in the body is live again on the next pass.
		{"func f() { xs := []int{1}; r := ref xs; for i := 0; i < 2; i += 1 { print(r); xs.Push(1) } }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r at 16:28, used in the next pass of the loop"}},
		{"func f() { xs := []int{1}; for i := 0; i < 2; i += 1 { r := ref xs; print(r); xs.Push(1) } }", nil},
		{"func f() { xs := []int{1}; r := ref xs; print(r); for i := 0; i < 2; i += 1 { xs.Push(1) } }", nil},
		{"func f(xs mut []int) { for _, x := range xs { xs.Push(*x) } }", []string{"cannot borrow xs as mut: xs is already borrowed as mut by the range loop"}},
		{"func f() { xs := []int{1}; for _, x := range ref xs { xs.Push(x) } }", []string{"cannot borrow xs as mut: xs is borrowed as ref by the range loop"}},
		{"func f() { s := \"a b\"; for w := range s.Fields() { s = \"x\"; read(w) } }", []string{"cannot assign to s: s is borrowed by the range loop"}},
		// Walking a chain: a borrow variable is re-pointed at a borrow derived from itself.
		{"func f(n mut Node, v string) { cur := mut n.next; for { m := cur ?? { *cur = Some(&Node{value: v, next: None, n: 0}); return }; cur = mut m.next } }", nil},
		{"func f(n mut Node) { a := mut n.next; b := mut n.next; print(a, b) }", []string{"cannot borrow n.next as mut: n.next is already borrowed as mut by a"}},
		{"func f(n mut Node) { cur := mut n.next; m := cur ?? return; print(cur); m.n = 1 }", []string{"cannot use cur: cur is borrowed as mut by m"}},
		{"func f(n mut Node) { cur := mut n.next; m := cur ?? return; x := mut n.next; m.n = 1; print(x) }", []string{"cannot borrow n.next as mut: n.next is already borrowed as mut by cur"}}, // live through m
		{"func f(n mut Node) { cur := mut n.next; m := cur ?? return; m.n = 1; x := mut n.next; print(x) }", nil},
		// A ref is read-only.
		{"func f(n ref Node) { n.value = \"x\" }", []string{"cannot assign to n.value through a ref borrow"}},
		{"func f(n ref Node) { n.n += 1 }", []string{"cannot assign to n.n through a ref borrow"}},
		{"func (t ref Tree) f() { t.nodes.Push(Node{value: \"a\", next: None, n: 0}) }", []string{"cannot borrow t.nodes as mut through a ref receiver"}},
		{"func f(t ref Tree) { t.nodes.Push(Node{value: \"a\", next: None, n: 0}) }", []string{"cannot borrow t.nodes as mut through a ref borrow"}},
		{"func f(n ref Node) { take(mut n.next) }", []string{"cannot borrow n.next as mut through a ref borrow"}},
		{"func (n mut Node) Bump() { n.n += 1 }\nfunc f(n ref Node) { n.Bump() }", []string{"cannot borrow n as mut: n is a ref borrow"}}, // the variable is itself a ref
		{"func (n mut Node) Bump() { n.n += 1 }\nfunc f(n ref Node) { m := n; m.Bump() }", []string{"cannot borrow m as mut: m is a ref borrow"}},
		{"func (n mut Node) Bump() { n.n += 1 }\nfunc (n ref Node) Twice() { n.Bump() }", []string{"cannot borrow n as mut: n is a ref receiver"}},
		{"func (n mut Node) Bump() { n.n += 1 }\nfunc f(n mut Node) { n.Bump() }", nil},
		// Matching through a borrow: the bindings hold it; a Copy part of a place is copied.
		{"func f(st mut State) { match st { Failed(r) => { *st = Closed; keep(r.Clone()) }, _ => {} } }", []string{"cannot assign to *st: st is borrowed by r"}},
		{"func f(st ref State) { match st { Failed(r) => keep(r.Clone()), _ => {} } }", nil},
		{"func f(t mut Tree) { if n := t.nodes[0].next { t.nodes.Push(Node{value: \"a\", next: None, n: n.n }) } }", nil}, // n's last use is an argument, before the receiver is borrowed
		{"func f(t mut Tree) { if n := t.nodes[0].next { t.nodes.Push(Node{value: \"a\", next: None, n: 0 }); n.n = 1 } }", []string{"cannot borrow t.nodes as mut: t.nodes is already borrowed as mut by n"}},
		{"func f() { xs := []int{1}; r := ref xs; xs.Push(len(r)) }", nil},
		{"func f(t mut Tree, st mut State) { match t.nodes[0].n { 0 => match st { _ => {} }, k => *k = 1 } }", []string{"cannot dereference a value of type int"}}, // an inner match does not change the outer's bindings
		{"func f(n mut int) { n++ }", []string{"++ is not defined on mut int"}},
		{"func f(t mut Tree) { match t.nodes[0].n { k => t.nodes.Push(Node{value: \"a\", next: None, n: k }) } }", nil}, // k is an int, copied
		{"func f(t mut Tree) { match mut t.nodes[0] { n => n.n = 1 } }", nil},                                           // written mut, n is a mut Node
		{"func f(t mut Tree) { match t.nodes[0].n { k => *k = 1 } }", []string{"cannot dereference a value of type int"}},
		{"func f(o mut (int?)) { if n := o { *n += 1 } }", nil}, // through a borrow variable, n is a mut int
		{"func f(m mut map[string]Node) { match m[\"k\"] { n => n.n = 1 } }", []string{"cannot bind mut through m[\"k\"]"}},
		// A borrow returned by a method holds its receiver.
		{"func f(m mut map[string]int) { v := m.Get(\"a\"); m.Insert(\"b\", 1); print(v) }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f(m mut map[string]int) { v := m.Get(\"a\"); print(v); m.Insert(\"b\", 1) }", nil},
		{"func f(m mut map[string]Node) { e := m.GetMut(\"a\").Expect(\"x\"); m.GetMut(\"b\").Expect(\"y\").n = e.n }", []string{"cannot borrow m as mut: m is already borrowed as mut by e"}},
		{"func f(m mut map[string]Node) { e := m.GetMut(\"a\").Expect(\"x\"); n := e.n; m.GetMut(\"b\").Expect(\"y\").n = n }", nil}, // copy out first
		{"func f() { s := \" a \"; t := s.Trim(); s = \"b\"; read(t) }", []string{"cannot assign to s: s is borrowed by t"}},
		// A borrow bound out of a call's option by if or match holds the call's receiver, as one bound by := does.
		{"func f(m mut map[string]int) { if v := m.Get(\"a\") { m.Insert(\"b\", 1); print(v) } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f(m mut map[string]int) { if v := m.Get(\"a\") { print(v); m.Insert(\"b\", 1) } }", nil},
		{"func f(m mut map[string]Node) { if e := m.GetMut(\"a\") { if g := m.GetMut(\"b\") { g.n = e.n } } }", []string{"cannot borrow m as mut: m is already borrowed as mut by e"}},
		{"func f(m mut map[string]int) { match m.Get(\"a\") { Some(v) => { m.Insert(\"b\", 1); print(v) }, None => {} } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f(m mut map[string]Node) { match m.GetMut(\"a\") { Some(e) => { m.Remove(\"a\"); e.n = 1 }, None => {} } }", []string{"cannot borrow m as mut: m is already borrowed as mut by e"}},
		{"func f(xs mut []int) { if v := xs.Pop() { xs.Push(v) } }", nil}, // Pop's option owns its value: v holds nothing
		// A borrow returned by a function holds the argument passed to its borrowed parameter, wherever that parameter stands.
		{"func f() { m := map[string]string{}; v := pick(0, ref m); m.Remove(\"a\"); print(v) }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f() { m := map[string]string{}; if v := pick(0, ref m) { m.Remove(\"a\"); print(v) } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f() { m := map[string]string{}; match pick(0, ref m) { Some(v) => { m.Remove(\"a\"); print(v) }, None => {} } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f() { m := map[string]string{}; v := pick(0, ref m); print(v); m.Remove(\"a\") }", nil},
		{"func f() { m := map[string]string{}; if v := pick(0, ref m) { print(v) }; m.Remove(\"a\") }", nil},
		{"func f(m mut map[string]string) { if v := find(m) { m.Remove(\"a\"); print(v) } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f(m mut map[string]string) { match find(m) { Some(v) => { m.Remove(\"a\"); print(v) }, None => {} } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f(m mut map[string]string) { if v := find(m) { print(v) }; m.Remove(\"a\") }", nil},
		{"func f() { a := \"x\"; b := \"y\"; p := Both(ref a, ref b); b = \"z\"; print(p) }", []string{"cannot assign to b: b is borrowed by p"}}, // a variant holds every borrow passed to it
		// The else branch and the None arm hold nothing: the binding is not in scope there.
		{"func f(m mut map[string]int, k ref string) { if v := m.Get(k) { print(v) } else { m.Insert(k.Clone(), 1) } }", nil},
		{"func f(m mut map[string]int, k ref string) { match m.Get(k) { Some(v) => print(v), None => m.Insert(k.Clone(), 1) } }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+"func bump(a mut int, b mut int) { *a += 1; *b += 1 }\nstruct Pair { a string; b string }\nstruct Tree { nodes []Node }\nenum Two { Both(a ref string, b ref string) }\nfunc pick(n int, m ref map[string]string) ref string? { return m.Get(\"a\") }\nfunc find(m ref map[string]string) ref string? { return m.Get(\"a\") }\n"+tt.src)
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

// TestLending checks what mut may lend and how long a borrow a call
// returns stays lent: a ref borrow, and a slice of a value reached
// through one, are never lent mut, and a borrow passed from one call
// to another keeps its place lent until the call it goes to returns.
func TestLending(t *testing.T) {
	const helpers = "struct Tree { nodes []Node }\nfunc find(m ref map[string]string) ref string? { return m.Get(\"a\") }\n" +
		"func sort(xs mut []int) {}\nfunc touch(ns mut []Node) {}\nfunc clear(m mut map[string]string) int { return 0 }\n" +
		"func both(v ref string?, n int) {}\nfunc hand(v ref string?, m mut map[string]string) {}\n" +
		"func pass(v ref string, n int) {}\nfunc wipe(s mut string) int { return 0 }\n"
	tests := []struct {
		src  string
		want []string
	}{
		// mut lends what an owned value or a mut borrow reaches, never what a ref borrow does.
		{"func f(s ref string) { t := take(mut s); keep(t) }", []string{"cannot borrow s as mut: s is a ref borrow"}},
		{"func f() { s := \"a\"; r := ref s; t := take(mut r); keep(t) }", []string{"cannot borrow r as mut: r is a ref borrow"}},
		{"func f(xs ref []int) { grow(mut xs) }", []string{"cannot borrow xs as mut: xs is a ref borrow"}},
		{"func f(xs ref []int) { sort(mut xs[0:1]) }", []string{"cannot borrow xs[0:1] as mut: it is a slice of xs, reached through a ref borrow"}},
		{"func f(t ref Tree) { touch(mut t.nodes[1:]) }", []string{"cannot borrow t.nodes[1:] as mut: it is a slice of t.nodes, reached through a ref borrow"}},
		{"func f(xs ref []int) { ys := xs[0:1]; sort(mut ys) }", []string{"cannot borrow ys as mut: ys is a ref borrow"}},
		{"func f(xs ref []int) { sort(mut (xs[0:1])) }", []string{"cannot borrow xs[0:1] as mut: it is a slice of xs, reached through a ref borrow"}},
		{"func f(xs mut []int) { sort(mut xs[0:1]) }", nil},
		{"func f() { xs := []int{2, 1}; sort(mut xs[0:2]); r := mut xs; grow(mut r) }", nil},
		{"func f(t mut Tree) { touch(mut t.nodes[1:]) }", nil},
		// A borrow a call returns stays lent while it is passed along: the call it goes to sees no mut of the same place.
		{"func f() { m := map[string]string{}; both(find(ref m), clear(mut m)) }", []string{"cannot borrow m as mut: m is borrowed as ref at"}},
		{"func f() { m := map[string]string{}; print(find(ref m), clear(mut m)) }", []string{"cannot borrow m as mut: m is borrowed as ref at"}},
		{"func f() { m := map[string]string{}; hand(find(ref m), mut m) }", []string{"cannot borrow m as mut: m is borrowed as ref at"}},
		{"func f() { m := map[string]string{}; both(m.Get(\"a\"), clear(mut m)) }", []string{"cannot borrow m as mut: m is borrowed as ref at"}},
		{"func f() { m := map[string]string{}; both(find(ref m), 1); clear(mut m); print(find(ref m), find(ref m)) }", nil},
		{"func f() { m := map[string]int{}; m.Insert(\"b\", *(m.Get(\"a\") ?? ref 0) + 1) }", nil}, // the borrow is read and gone before the call lends m
		// A value chosen at run time borrows from every place it may come from: each branch of an if or match, both sides of ??.
		{"func f(c bool) { s := \"a\"; z := \"z\"; x := if c { ref s } else { ref z }; s = \"b\"; print(x) }", []string{"cannot assign to s: s is borrowed by x"}},
		{"func f(c bool) { s := \"a\"; z := \"z\"; x := if c { ref s } else { ref z }; z = \"b\"; print(x) }", []string{"cannot assign to z: z is borrowed by x"}},
		{"func f(c bool) { s := \"a\"; z := \"z\"; x := match c { true => ref s, false => ref z }; s = \"b\"; print(x) }", []string{"cannot assign to s: s is borrowed by x"}},
		{"func f(o ref string?) { z := \"z\"; x := o ?? ref z; z = \"y\"; print(x) }", []string{"cannot assign to z: z is borrowed by x"}},
		{"func f(c bool) { s := \"a\"; z := \"z\"; pass(if c { ref s } else { ref z }, wipe(mut s)) }", []string{"cannot borrow s as mut: s is borrowed as ref at"}},
		{"func f(c bool) { xs := []int{1}; ys := []int{}; for _, v := range if c { ref xs } else { ref ys } { xs.Push(v) } }", []string{"cannot borrow xs as mut: xs is borrowed as ref by the range loop"}},
		{"func f(c bool) { x := if c { \"a\".Clone().Trim() } else { \"b\".Trim() }; print(x) }", []string{"cannot keep a borrow of a temporary"}},
		{"func f(c bool, s ref string) ref string { z := \"z\"; return if c { s } else { ref z } }", []string{"cannot return a borrow of local z"}},
		{"func f(c bool) { s := \"a\"; z := \"z\"; x := if c { ref s } else { ref z }; print(x); s = \"b\"; z = \"c\" }", nil},
		{"func f(c bool) int { s := \"a\"; n := if c { len(s) } else { 0 }; s = \"b\"; return n }", nil}, // a Copy value borrows nothing
		{"func f(o ref string?) ref string { return o ?? return \"x\" }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+helpers+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%q: got %d errors %q, want %q", tt.src, len(got), got, tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%q: error %q does not contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
