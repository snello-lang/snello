// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestLowerCalls pins the form of calls by what the callee is and how
// the receiver and the arguments are given: a method on an owned place
// reads the receiver first and lends it after the arguments; a mut
// method the same; a variadic call gives each extra argument to the
// element type and a spread gives the slice; a variant written as a
// call makes its value; a closure held in a variable is read before
// its arguments; and a temporary receiver given by value is the
// callee's, not dropped by the statement.
func TestLowerCalls(t *testing.T) {
	const decls = "struct B { n int; name string }\nenum State { Closed; Open(id int) }\n" +
		"func (b ref B) Get(k int) int { return b.n }\nfunc (b mut B) Set(k int) { b.n = k }\nfunc (b B) Consume() int { return 0 }\n" +
		"func taken(s string) int { return 0 }\nfunc many(xs ...string) {}\nfunc mkb() B { return B{n: 0, name: \"b\"} }\n"
	tests := []struct{ src, want string }{
		{"func f() { n := mkb().Consume(); print(n) }", `b0:
  t0 = call()
  t1 = call(t0)
  store n <- t1
  read n
  t2 = call(n)
  leave(n)
  ->
`},
		{"func f(b B, s string) { n := b.Get(taken(s)); print(n) }", `b0:
  read b
  move s
  t0 = call(s)
  t1 = borrow ref b
  t2 = call(t1, t0)
  store n <- t2
  read n
  t3 = call(n)
  leave(n, s, b)
  ->
`},
		{"func f(s string) { b := B{n: 0, name: s}; b.Set(1) }", `b0:
  t0 = make()
  move s
  t1 = make(t0, s)
  store b <- t1
  read b
  t2 = make()
  t3 = borrow mut b
  t4 = call(t3, t2)
  leave(b, s)
  ->
`},
		{"func f(a string, ys []string) { many(a.Clone(), a); many(ys...) }", `b0:
  read a
  t0 = borrow ref a
  t1 = call(t0)
  move a
  t2 = call(t1, a)
  move ys
  t3 = call(ys)
  leave(ys, a)
  ->
`},
		{"func f(s string) { st := Open(len(s)); print(st) }", `b0:
  read s
  t0 = call(s)
  t1 = make(t0)
  store st <- t1
  read st
  t2 = call(st)
  leave(st, s)
  ->
`},
		{"func f(g func(string) int, s string) { n := g(s); print(n) }", `b0:
  read g
  move s
  t0 = call(s)
  store n <- t0
  read n
  t1 = call(n)
  leave(n, s, g)
  ->
`},
	}
	for _, tt := range tests {
		if got := lowered(t, "package p\n"+decls+tt.src); got != tt.want {
			t.Errorf("%s\nlowered:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}
