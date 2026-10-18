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
		{"func f() { xs := []int{1}; r := ref xs; for i := 0; i < 2; i += 1 { print(r); xs.Push(1) } }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r at 13:28, used in the next pass of the loop"}},
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
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+"func bump(a mut int, b mut int) { *a += 1; *b += 1 }\nstruct Pair { a string; b string }\nstruct Tree { nodes []Node }\n"+tt.src)
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
