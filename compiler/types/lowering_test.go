// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
)

// lowered checks src and returns the printed form of its function f.
func lowered(t *testing.T, src string) string {
	t.Helper()
	f, file, err := parser.ParseFile("form.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{Forms: map[*ast.BlockStmt]*Form{}}
	if _, err := Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
			return info.Forms[fd.Body].String()
		}
	}

	t.Fatal("no function f")
	return ""
}

// TestLowering pins the form of programs whose order the language
// defines:
// a call's argument moved, an index operand before the value and the
// place stored to without a read of what it is reached through, a
// loop's head, test, body and post, an if expression's arms storing
// into one temporary, a place reached through a call after the value, a
// compound assignment's read, its second reach of what the call reads
// and its store, a swap's values before its stores, ?? and ? leaving
// with their drops, a range over an owned slice moving it, and a closure
// made from a borrow of what it captures.
func TestLowering(t *testing.T) {
	tests := []struct{ src, want string }{
		{"func take(s string) int { return 0 }\nfunc f(s string, xs []int) { n := take(s); xs[0] = n; print(n) }", `b0:
  move s
  t0 = call(s)
  store n <- t0
  t1 = make()
  read n
  store xs.[] <- n
  read n
  t2 = call(n)
  leave(n, xs, s)
  ->
`},
		{"func f(v []string) { r := mut v[0]; *r = \"z\".Clone(); for i := 0; i < 2; i += 1 { print(i) } }", `b0:
  t0 = make()
  t1 = borrow mut v.[]
  store r <- t1
  t2 = make()
  t3 = borrow ref t2
  t4 = call(t3)
  store r.* <- t4
  t5 = make()
  store i <- t5
  -> b1
b1 (head):
  read i
  t6 = make()
  t7 = make(i, t6)
  -> b2 b3
b2:
  drop i
  leave(r, v)
  ->
b3:
  read i
  t8 = call(i)
  -> b4
b4:
  read i
  t9 = make()
  t10 = make(i, t9)
  store i <- t10
  -> b1
`},
		{"func f(c bool, s string) string { return if c { s } else { \"b\".Clone() } }", `b0:
  read c
  -> b1 b2
b1:
  move s
  store t0 <- s
  -> b3
b2:
  t1 = make()
  t2 = borrow ref t1
  t3 = call(t2)
  store t0 <- t3
  -> b3
b3:
  read t0
  return(t0)
  leave(s, c)
  ->
`},
		{"struct B { n int; name string }\nfunc pick(b mut B, k int) mut B { return b }\nfunc read(s ref string) int { return len(s) }\nfunc taken(s string) int { return 0 }\nfunc f(s string) { b := B{n: 0, name: \"b\"}; pick(mut b, read(s)).n = len(s); print(taken(s)) }", `b0:
  t0 = make()
  t1 = make()
  t2 = make(t0, t1)
  store b <- t2
  read s
  t3 = call(s)
  t4 = borrow mut b
  t5 = borrow ref s
  t6 = call(t5)
  t7 = call(t4, t6)
  store t7.n <- t3
  drop t7
  move s
  t8 = call(s)
  t9 = call(t8)
  leave(b, s)
  ->
`},
		{"func rows(xs mut []int) mut []int { return xs }\nfunc f(xs []int) { rows(mut xs)[0] += len(xs) }", `b0:
  t0 = borrow mut xs
  t1 = call(t0)
  t2 = make()
  read t1.[]
  read xs
  t3 = call(xs)
  t4 = make(t1.[], t3)
  read xs
  store t1.[] <- t4
  drop t1
  leave(xs)
  ->
`},
		{"func f(i int, s string) { m := map[int]string{i: s}; print(len(m)) }", `b0:
  read i
  move s
  t0 = make(i, s)
  store m <- t0
  read m
  t1 = call(m)
  t2 = call(t1)
  leave(m, s, i)
  ->
`},
		{"func f(a string, b string) { a, b = b, a; print(a, b) }", `b0:
  move b
  t0 = make(b)
  move a
  t1 = make(a)
  store a <- t0
  store b <- t1
  read a
  read b
  t2 = call(a, b)
  leave(b, a)
  ->
`},
		{"func get(o int?) int? { return o }\nfunc f(o int?) int? { n := o ?? return None; m := get(o)?; return Some(n + m) }", `b0:
  read o
  -> b1 b2
b1:
  t0 = make(o)
  -> b3
b2:
  t1 = make()
  return(t1)
  leave(o)
  ->
b3:
  read t0
  store n <- t0
  read o
  t2 = call(o)
  -> b4 b5
b4:
  t3 = make(t2)
  -> b6
b5:
  return(t2)
  leave(n, o)
  ->
b6:
  read t3
  store m <- t3
  read n
  read m
  t4 = make(n, m)
  t5 = make(t4)
  return(t5)
  leave(m, n, o)
  ->
`},
		{"struct Note impl Drop { name string }\nfunc (n mut Note) Drop() {}\nfunc f(xs []int) { for _, x := range xs { print(x) }; n := Note{name: \"a\".Clone()}; g := func() { print(n.name) }; g() }", `b0:
  move xs
  -> b1
b1 (head):
  -> b2 b3
b2:
  t1 = make()
  t2 = borrow ref t1
  t3 = call(t2)
  t4 = make(t3)
  store n <- t4
  t5 = borrow ref n
  t6 = make(t5)
  store g <- t6
  read g
  t7 = call()
  leave(g, n, xs)
  ->
b3:
  store x <- xs.[]
  read x
  t0 = call(x)
  drop x
  -> b1
`},
	}
	for _, tt := range tests {
		if got := lowered(t, "package p\n"+tt.src); got != tt.want {
			t.Errorf("%s\nlowered:\n%s\nwant:\n%s", tt.src, got, tt.want)
		}
	}
}

// BenchmarkLowering checks every example program as BenchmarkCheck
// does, once recording what the checker learned and once keeping each
// body's form as well, and reports allocations: the difference between
// the two is what the forms kept cost; the lowering and the passes over
// the form run in both.
func BenchmarkLowering(b *testing.B) {
	type parsed struct {
		f    *ast.File
		file *token.File
	}

	var files []parsed
	for _, path := range examplePrograms(b) {
		f, file, _ := parseFile(b, path)
		files = append(files, parsed{f, file})
	}

	conf := libraryConfig()
	for _, bench := range []struct {
		name string
		info func() *Info
	}{
		{"recorded", func() *Info { return &Info{} }},
		{"forms", func() *Info { return &Info{Forms: map[*ast.BlockStmt]*Form{}} }},
	} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, p := range files {
					if _, err := conf.Check("", p.file, p.f, bench.info()); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
