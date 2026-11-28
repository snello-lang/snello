// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
)

// TestLowerExpressions pins the form of the expressions whose use of a
// place the type it is given to decides: a literal's part given to a
// field of borrow type is lent and one given to a slice's element is
// moved; a conversion to the same value under another name moves and
// one between a string and its bytes reads; ?? moves an owned payload
// and makes the value on its two paths into one temporary, the option
// a call made taken by the result and not dropped; an if among a
// literal's parts drops nothing of the parts made before it; a match
// guard's blocks hold what the guard evaluates, the arm's body and the
// path to the next arm leaving them; a value made here, the value an
// if chooses and a slice of one, given where a borrow is wanted, is
// lent, the statement dropping it at its end; and a borrow bound
// through a borrow of it, given where the borrow is wanted, is read
// through the binding.
func TestLowerExpressions(t *testing.T) {
	const decls = "struct Row { name string }\nstruct L { r ref Row; n int }\nstruct P { a string; n int }\ntype Name string\nenum E { A(string); B(int) }\n" +
		"func keep(s string) {}\nfunc take(s string) int { return 0 }\nfunc mk() string { return \"a\".Clone() }\nfunc opt() string? { return None }\nfunc id(x ref string) ref string { return x }\n"
	tests := []struct{ src, want string }{
		{"func f() { e := A(mk()); keep(\"x\") }", `b0:
  t0 = call()
  t1 = make(t0)
  store e <- t1
  t2 = make()
  t3 = call(t2)
  leave(e)
  ->
`},
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
  read t1
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
  t3 = borrow ref t2
  t4 = call(t3)
  store t1 <- t4
  -> b3
b3:
  read t1
  store s <- t1
  move s
  t5 = call(s)
  leave(s)
  ->
`},
		{"func f(row Row, s string) { l := L{r: row, n: 1}; print(l.n); ys := []string{s}; print(len(ys)) }", `b0:
  t0 = borrow ref row
  t1 = make()
  t2 = make(t0, t1)
  store l <- t2
  drop t0
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
  t2 = borrow ref t1
  t3 = call(t2)
  store t0 <- t3
  -> b3
b3:
  read t0
  store s <- t0
  move s
  t4 = call(s)
  leave(s, o)
  ->
`},
		{"func f(n int, s string) int { return match n { 1 if take(s.Clone()) > 0 => 1, _ => 2 } }", `b0:
  read n
  -> b2 b3
b1:
  read t0
  return(t0)
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
		{"func f() { print(id(mk())) }", `b0:
  t0 = call()
  t1 = borrow ref t0
  t2 = call(t1)
  t3 = call(t2)
  drop t0
  leave()
  ->
`},
		{"func f(c bool) { print(id(if c { \"a\" } else { \"b\" })) }", `b0:
  read c
  -> b1 b2
b1:
  t1 = make()
  store t0 <- t1
  -> b3
b2:
  t2 = make()
  store t0 <- t2
  -> b3
b3:
  read t0
  t3 = borrow ref t0
  t4 = call(t3)
  t5 = call(t4)
  drop t0
  leave(c)
  ->
`},
		{"func f(p ref string) ref string { o := Some(p); return match ref o { Some(x) => x, None => p } }", `b0:
  read p
  t0 = borrow ref p
  t1 = make(t0)
  store o <- t1
  t3 = borrow ref o
  -> b2 b3
b1:
  read t2
  return(t2)
  drop t3
  leave(o, p)
  ->
b2:
  store x <- t3.?Some.?0
  read x
  store t2 <- x
  drop x
  -> b1
b3:
  -> b4
b4:
  read p
  store t2 <- p
  -> b1
`},
		{"func f() { print(id(mk()[0:1])) }", `b0:
  t0 = make()
  t1 = make()
  t2 = call()
  t3 = borrow ref t2
  t4 = call(t3)
  t5 = call(t4)
  drop t2
  leave()
  ->
`},
	}
	for _, tt := range tests {
		if got := lowered(t, "package p\n"+decls+tt.src); got != tt.want {
			t.Errorf("%s\nlowered:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}

// TestFormCaptures reads a closure's captures off its form: a variable
// read is borrowed, one assigned or lent mut is borrowed mut, one moved
// is moved, and a Copy value only read is copied in; a variable the
// closure declares is its own.
func TestFormCaptures(t *testing.T) {
	src := prelude + "func f(r string, m string, v string, n int) { g := func() { print(r); m = \"x\"; keep(v); print(n); own := 1; print(own) }; g() }"
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{Forms: map[*ast.BlockStmt]*Form{}}
	if _, err := libraryConfig().Check("", file, f, info); err != nil {
		t.Fatal(err)
	}

	var lit *ast.FuncLit
	ast.Inspect(f, func(n ast.Node) bool {
		if l, ok := n.(*ast.FuncLit); ok {
			lit = l
		}

		return true
	})
	want := map[string]captureMode{"r": byRef, "m": byMut, "v": byMove, "n": byCopy}
	got := formCaptures(info.Forms[lit.Body], lit.Pos())
	if len(got) != len(want) {
		t.Fatalf("got %d captures, want %d", len(got), len(want))
	}

	for _, cap := range got {
		if mode, ok := want[cap.v.name]; !ok || mode != cap.mode {
			t.Errorf("%s captured as %d, want %d", cap.v.name, cap.mode, mode)
		}
	}
}
