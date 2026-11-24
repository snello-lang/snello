// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestLowerPlaces pins the paths of places: a store through a mut
// parameter writes the field behind the borrow without a read of it, an
// index operand is evaluated before the value and the element stored
// to, a written deref is the same place as the bare field; a Copy field
// of a value a call returns is read from the temporary, which the
// statement then drops; and a field of a pointer or an owned value is
// moved out by its path.
func TestLowerPlaces(t *testing.T) {
	const decls = "struct Node { value string; next *Node?; n int }\n" +
		"func mkp() *Node { return &Node{value: \"a\", next: None, n: 1} }\n"
	tests := []struct{ src, want string }{
		{"func f(p mut Node, xs []int, i int) { p.next = None; xs[i] = 1; (*p).n = 2 }", `b0:
  t0 = make()
  store p.*.next <- t0
  read i
  t1 = make()
  store xs.[] <- t1
  t2 = make()
  store p.*.n <- t2
  leave(i, xs)
  ->
`},
		{"func f() { n := (*mkp()).n; print(n) }", `b0:
  t0 = call()
  read t0.*.n
  store n <- t0.*.n
  drop t0
  read n
  t1 = call(n)
  leave(n)
  ->
`},
		{"func f(p *Node, q Node) { s := p.value; t := q.value; print(len(s), len(t)) }", `b0:
  move p.*.value
  store s <- p.*.value
  move q.value
  store t <- q.value
  read s
  t0 = call(s)
  read t
  t1 = call(t)
  t2 = call(t0, t1)
  leave(t, s, q, p)
  ->
`},
	}
	for _, tt := range tests {
		if got := lowered(t, "package p\n"+decls+tt.src); got != tt.want {
			t.Errorf("%s\nlowered:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}
