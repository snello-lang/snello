// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestEscapes checks the escape pass rule by rule: what owned data may
// hold, where a returned borrow may come from, and what a closure that
// captures a local may not do. The negative programs under
// examples/core cover the messages; these cover the edges between them.
func TestEscapes(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// Owned data cannot hold a borrow, whatever carries it.
		{"func f() { xs := []ref string{} }", []string{"a slice cannot hold a borrow"}},
		{"func f() { xs := []Lexer{} }", []string{"a slice cannot hold Lexer: it holds a borrow"}},
		{"func f() { m := map[string]ref int{} }", []string{"a map cannot hold a borrow"}},
		{"func f() { ch := make(chan ref int) }", []string{"a channel cannot carry a borrow"}},
		{"func f(p *ref int) {}", []string{"a pointer cannot hold a borrow"}},
		{"func f(xs [2]ref int) {}", []string{"an array cannot hold a borrow"}},
		{"func f(xs []ref int?) {}", []string{"a slice cannot hold a borrow"}},
		{"func f() []ref int { return []ref int{} }", []string{"a slice cannot hold a borrow", "a slice cannot hold a borrow"}},
		{"struct S { xs []Lexer }", []string{"a slice cannot hold Lexer: it holds a borrow"}},
		{"struct S { l Lexer; o ref int? }", nil}, // a struct holding a borrow is a borrow, which is allowed
		{"func f(s ref string) Lexer { return Lexer{src: s, pos: 0} }", nil},
		{"func f(s ref string, n int) Lexer { return Lexer{src: s, pos: n} }", nil}, // a count beside the borrow carries none
		{"struct Box { v int }\nfunc (b ref Box) Get() ref int { return ref b.v }\nfunc f() ref int { b := Box{v: 1}; return b.Get() }", []string{"cannot return a borrow of local b"}},
		{"func first(xs ref []int) ref int { return ref xs[0] }\nfunc f() ref int { xs := []int{1}; return first(xs) }", []string{"cannot return a borrow of local xs"}},
		{"func f() ref int { xs := []int{1}; return xs[0] }", []string{"cannot return a borrow of local xs"}},
		{"func f() ref string { s := \"ab\".Clone(); return s.Trim() }", []string{"cannot return a borrow of local s"}},
		{"func f(s ref string) Lexer { l := Lexer{src: s, pos: 0}; return l }", nil},
		{"func f(s ref string) ref Lexer { l := Lexer{src: s, pos: 0}; return ref l }", []string{"cannot return a borrow of local l"}},
		{"func f() Lexer { s := \"a\"; return Lexer{src: ref s, pos: 0} }", []string{"cannot return a borrow of local s"}},
		{"func f() Lexer { s := \"a\"; l := Lexer{src: ref s, pos: 0}; return l }", []string{"cannot return a borrow of local s"}},
		{"func f() { s := \"a\"; l := Lexer{src: ref s, pos: 0}; s = \"b\"; print(l) }", []string{"cannot assign to s: s is borrowed by l"}},
		{"func f(s ref string) ref string { return trim(1, s) }", nil}, // the callee's borrowed parameter is the second
		{"func f() ref string { return ref g }", []string{"cannot return a borrow of g: a returned borrow is tied to the receiver or a borrowed parameter"}},
		// A returned borrow has one source.
		{"func first(n ref Node) ref string { return ref n.value }", nil},
		{"func (n ref Node) first() ref string { return ref n.value }", nil},
		{"func pick(a ref Node, b ref Node) ref string { return ref a.value }", []string{"pick returns a borrow but has two borrowed parameters"}},
		{"func (n ref Node) pick(m ref Node) ref string { return ref m.value }", []string{"cannot return a borrow of m: a method's returned borrow is tied to its receiver"}},
		{"func f() ref string { s := \"a\"; return ref s }", []string{"cannot return a borrow of local s"}},
		{"func f() ref string { s := \"a\"; return s.Clone().Trim() }", []string{"cannot return a borrow of a temporary: s.Clone() is dropped when the statement ends"}},
		{"func f() ref string { return trim(1, \"y\".Clone()) }", []string{"cannot return a borrow of a temporary: \"y\".Clone() is dropped when the statement ends"}},
		{"func f() Lexer { return Lexer{src: \"z\".Clone().Trim(), pos: 0} }", []string{"cannot return a borrow of a temporary: \"z\".Clone() is dropped when the statement ends"}},
		{"func f(s ref string) ref string { return s.Trim() }", nil},
		{"func f(n Node) ref string { return ref n.value }", []string{"cannot return a borrow of local n"}},
		{"func f(n ref Node) ref string { s := n.value.Clone(); return ref s }", []string{"cannot return a borrow of local s"}},
		{"func f(n ref Node) ref string { t := ref n.value; return t }", nil}, // through a holder
		{"func f(n ref Node) ref string { s := \"a\"; t := ref s; return t }", []string{"cannot return a borrow of local s"}},
		{"func f(n mut Node) mut (*Node?) { cur := mut n.next; m := cur ?? return cur; return mut m.next }", nil}, // down a chain
		{"func f(n ref Node) string? { s := \"a\"; return Some(s) }", nil},
		{"func f(n ref Node) ref string? { s := \"a\"; return Some(ref s) }", []string{"cannot return a borrow of local s"}},
		{"func f(n ref Node) (ref string, int) { return (ref n.value, 1) }", nil},
		{"func f(n Node) (ref string, int) { return (ref n.value, 1) }", []string{"cannot return a borrow of local n"}},
		{"func f(xs ref []int) ref []int { return xs[1:2] }", nil},
		{"func f() ref []int { xs := []int{1}; return xs[0:1] }", []string{"cannot return a borrow of local xs"}},
		{"func f() ref []int { xs := []int{1}; return ref xs[0:1] }", []string{"cannot return a borrow of local xs"}}, // a slice expression views the place it slices
		{"func f() ref int { xs := []int{1}; return ref xs[0:1][0] }", []string{"cannot return a borrow of local xs"}},
		{"func f(xs []int) ref []int { return ref xs[0:1] }", []string{"cannot return a borrow of local xs"}},
		{"func f(xs ref []int) ref []int { return ref xs[0:1] }", nil},
		{"func f(b ref Buf) ref []int { return ref b.xs[0:1] }", nil},
		// A closure that captures a local is a borrow of it.
		{"func f() func() int { n := 0; return func() int { return n } }", nil}, // n is Copy and only read: copied in, no borrow
		{"func f() func() int { s := \"a\".Clone(); return func() int { return s.Len() } }", []string{"closure captures s by borrow and cannot be returned"}},
		{"func f() func() { n := 0; return func() { n += 1 } }", []string{"closure captures n by borrow and cannot be returned"}}, // assigned: borrowed mut
		{"func f() func() int { return func() int { return 1 } }", nil},
		{"func f() func() int { n := 0; g := func() int { return n }; return g }", nil},
		{"func f() func() int { s := \"a\".Clone(); g := func() int { return s.Len() }; return g }", []string{"closure g captures s by borrow and cannot be returned"}},
		{"func f() func() int { s := \"a\".Clone(); g := func() int { return s.Len() }; return func() int { return g() } }", []string{"closure captures g by borrow and cannot be returned"}}, // a function value is never copied in: its borrows are g's
		{"func f() func() { n := 0; g := func() { n += 1 }; return func() { g() } }", []string{"closure captures g by borrow and cannot be returned"}},
		{"func f() func() int { s := \"a\".Clone(); g := func() int { return s.Len() }; h := g; return h }", []string{"a closure that borrows cannot be copied to another variable"}}, // h holds what g held
		{"func f() func() int { n := 1; g := func() int { return n }; return func() int { return g() } }", []string{"closure captures g by borrow and cannot be returned"}},           // g itself is borrow-free, but a function value is captured by borrow of the frame holding it

		{"func f() { n := 0; g := func() { n += 1 }; xs := []func(){}; xs.Push(g) }", []string{"closure g captures n by borrow and cannot be stored"}},
		{"func f() { n := 0; xs := []func(){func() { n += 1 }} }", []string{"closure captures n by borrow and cannot be stored"}},
		{"func f() { n := 0; c := Cb{f: func() { n += 1 }} }", []string{"closure captures n by borrow and cannot be stored"}},
		{"func f() { c := Cb{f: func() {}}; c.f = func() {} }", nil},
		{"func f() { n := 0; c := Cb{f: func() {}}; c.f = func() { n += 1 } }", []string{"closure captures n by borrow and cannot be stored"}},
		{"func f() { n := 0; g := func() {}; g = func() { n += 1 }; g() }", []string{"a closure that borrows cannot be assigned with ="}}, // a variable, not owned data
		{"func f() { n := 0; ch := make(chan func(), 1); ch <- func() { n += 1 } }", []string{"closure captures n by borrow and cannot be sent"}},
		{"func f() { xs := []int{}; spawn count(ref xs) }", []string{"a spawned task cannot hold a borrow"}},
		{"func f() { xs := []int{}; spawn work(xs) }", nil},
		{"func f() { n := 0; g := func() { n += 1 }; spawn g() }", []string{"closure g captures n by borrow and cannot be spawned"}},
		{"func f() { n := 0; g := func() { n += 1 }; g(); print(n) }", nil},
		// The closure's borrow is live while the closure is used.
		{"func f() { xs := []int{}; g := func() { xs.Push(1) }; r := ref xs; g(); print(r) }", []string{"cannot borrow xs as ref: xs is already borrowed as mut by g"}},
		{"func f() { xs := []int{}; both(mut xs, func() { xs.Push(1) }) }", []string{"cannot borrow xs as mut: xs is already borrowed as mut"}},
		{"func f() { xs := []int{}; both(mut xs, func() {}) }", nil},
		{"func f() { s := \"a\"; g := func() { keep(s) }; g(); keep(s) }", []string{"use of moved value s"}}, // a moved capture is owned by the closure
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+"struct Lexer { src ref string; pos int }\nstruct Cb { f func() }\nstruct Buf { xs []int }\nfunc count(xs ref []int) {}\nfunc work(xs []int) {}\nfunc both(xs mut []int, f func()) {}\nfunc trim(n int, s ref string) ref string { return s }\nvar g string = \"x\"\n"+tt.src)
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
