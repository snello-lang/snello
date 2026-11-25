// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "testing"

// TestLowerStatements pins the form of the statements with a shape of
// their own: a select reading every case's channel and value in the
// header, the case taken alone moving the sent place, binding the
// received value or dropping it; a defer taking its arguments where it
// stands; an if binding an option's payload on its then path; a break
// leaving the body's names before the loop's exit drops the loop's; a
// var with a blank name whose value is made and not stored; a range
// over a channel lending it mut and over a string lending it ref; and a
// range over a value a call made, dropped where the loop ends and on a
// return out of the body, before the names the return drops.
func TestLowerStatements(t *testing.T) {
	const decls = "func keep(s string) {}\nfunc mk() string { return \"a\".Clone() }\n"
	tests := []struct{ src, want string }{
		{"func f(c bool) { for r := range mk() { if c { return }; print(r) }; print(1) }", `b0:
  t0 = call()
  -> b1
b1 (head):
  -> b2 b3
b2:
  drop t0
  t2 = make()
  t3 = call(t2)
  leave(c)
  ->
b3:
  store r <- t0.[]
  read c
  -> b4 b5
b4:
  drop t0
  leave(r, c)
  ->
b5:
  read r
  t1 = call(r)
  drop r
  -> b1
`},
		{"func f(c chan string, d chan string, s string) {\n\tselect {\n\tcase c <- s:\n\t\tprint(1)\n\tcase v := <-d:\n\t\tprint(v)\n\tcase <-d:\n\t\tprint(2)\n\t}\n}", `b0:
  read c
  read s
  read d
  t0 = make(d)
  read d
  t1 = make(d)
  -> b2 b3 b4
b1:
  leave(s, d, c)
  ->
b2:
  move s
  t2 = make()
  t3 = call(t2)
  -> b1
b3:
  store v <- t0
  read v
  t4 = call(v)
  drop v
  -> b1
b4:
  drop t1
  t5 = make()
  t6 = call(t5)
  -> b1
`},
		{"func f(s string) { defer keep(s) }", `b0:
  move s
  defer(s)
  leave(s)
  ->
`},
		{"func f(o string?) { if v := o { keep(v) } else { print(1) } }", `b0:
  move o
  -> b1 b3
b1:
  store v <- o.?Some.?0
  move v
  t0 = call(v)
  drop v
  -> b2
b2:
  leave(o)
  ->
b3:
  t1 = make()
  t2 = call(t1)
  -> b2
`},
		{"func f() { for i := 0; i < 2; i += 1 { n := \"a\".Clone(); if i == 0 { break }; keep(n) } }", `b0:
  t0 = make()
  store i <- t0
  -> b1
b1 (head):
  read i
  t1 = make()
  t2 = make(i, t1)
  -> b2 b3
b2:
  drop i
  leave()
  ->
b3:
  t3 = make()
  t4 = borrow ref t3
  t5 = call(t4)
  store n <- t5
  read i
  t6 = make()
  t7 = make(i, t6)
  -> b5 b6
b4:
  read i
  t9 = make()
  t10 = make(i, t9)
  store i <- t10
  -> b1
b5:
  leave(n)
  -> b2
b6:
  move n
  t8 = call(n)
  drop n
  -> b4
`},
		{"func f(s string) { var a, _ = s, 1; keep(a) }", `b0:
  move s
  t0 = make(s)
  t1 = make()
  store a <- t0
  move a
  t2 = call(a)
  leave(a, s)
  ->
`},
		{"func f(c chan string, w string) { for v := range c { keep(v) }; for r := range w { print(r) } }", `b0:
  t0 = borrow mut c
  -> b1
b1 (head):
  -> b2 b3
b2:
  drop t0
  t2 = borrow ref w
  -> b4
b3:
  store v <- t0.[]
  move v
  t1 = call(v)
  drop v
  -> b1
b4 (head):
  -> b5 b6
b5:
  drop t2
  leave(w, c)
  ->
b6:
  store r <- t2.[]
  read r
  t3 = call(r)
  drop r
  -> b4
`},
	}
	for _, tt := range tests {
		if got := lowered(t, "package p\n"+decls+tt.src); got != tt.want {
			t.Errorf("%s\nlowered:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}
