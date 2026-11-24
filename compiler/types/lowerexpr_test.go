// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestLowerExpressions pins the form of the expressions whose use of a
// place the type it is given to decides: a literal's part given to a
// field of borrow type is lent and one given to a slice's element is
// moved; a conversion to the same value under another name moves and
// one between a string and its bytes reads; ?? moves an owned payload
// and makes the value on its two paths into one temporary, the option
// a call made taken by the result and not dropped; an if among a
// literal's parts drops nothing of the parts made before it; and a
// match guard's blocks hold what the guard evaluates, the arm's body
// and the path to the next arm leaving them.
func TestLowerExpressions(t *testing.T) {
	const decls = "struct Row { name string }\nstruct L { r ref Row; n int }\nstruct P { a string; n int }\ntype Name string\n" +
		"func keep(s string) {}\nfunc take(s string) int { return 0 }\nfunc mk() string { return \"a\".Clone() }\nfunc opt() string? { return None }\n"
	tests := []struct{ src, want string }{
		{"func f(c bool) { x := P{a: mk(), n: if c { 1 } else { 2 }}; keep(x.a) }", `b0:
  t0 = call()
  read c
  -> b1 b2
b1:
  t2 = make()
  store t1 <- t2
  -> b3
b2:
  t3 = make()
  store t1 <- t3
  -> b3
b3:
  t4 = make(t0, t1)
  store x <- t4
  move x.a
  t5 = call(x.a)
  leave(x, c)
  ->
`},
		{"func f() { s := opt() ?? \"d\".Clone(); keep(s) }", `b0:
  t0 = call()
  -> b1 b2
b1:
  t1 = make(t0)
  -> b3
b2:
  t2 = make()
  t3 = call(t2)
  store t1 <- t3
  -> b3
b3:
  store s <- t1
  move s
  t4 = call(s)
  leave(s)
  ->
`},
		{"func f(row Row, s string) { l := L{r: row, n: 1}; print(l.n); ys := []string{s}; print(len(ys)) }", `b0:
  t0 = borrow ref row
  t1 = make()
  t2 = make(t0, t1)
  store l <- t2
  read l.n
  t3 = call(l.n)
  move s
  t4 = make(s)
  store ys <- t4
  read ys
  t5 = call(ys)
  t6 = call(t5)
  leave(ys, l, s, row)
  ->
`},
		{"func f(s string, t string) { n := Name(s); b := []byte(t); print(len(n), len(b)) }", `b0:
  move s
  t0 = make(s)
  store n <- t0
  read t
  t1 = make(t)
  store b <- t1
  read n
  t2 = call(n)
  read b
  t3 = call(b)
  t4 = call(t2, t3)
  leave(b, n, t, s)
  ->
`},
		{"func f(o string?) { s := o ?? \"d\".Clone(); keep(s) }", `b0:
  move o
  -> b1 b2
b1:
  t0 = make(o)
  -> b3
b2:
  t1 = make()
  t2 = call(t1)
  store t0 <- t2
  -> b3
b3:
  store s <- t0
  move s
  t3 = call(s)
  leave(s, o)
  ->
`},
		{"func f(n int, s string) int { return match n { 1 if take(s.Clone()) > 0 => 1, _ => 2 } }", `b0:
  read n
  -> b2 b3
b1:
  leave(s, n)
  ->
b2:
  -> b4
b3:
  -> b7
b4:
  read s
  t1 = borrow ref s
  t2 = call(t1)
  t3 = call(t2)
  t4 = make()
  t5 = make(t3, t4)
  -> b5 b6
b5:
  t6 = make()
  store t0 <- t6
  -> b1
b6:
  -> b3
b7:
  t7 = make()
  store t0 <- t7
  -> b1
`},
	}
	for _, tt := range tests {
		if got := lowered(t, "package p\n"+decls+tt.src); got != tt.want {
			t.Errorf("%s\nlowered:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}
