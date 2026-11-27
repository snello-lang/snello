// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"
	"testing"
)

// TestFormBorrows runs the borrow pass over the form beside the tree's
// on programs whose paths the pass has rules for, and wants the two to
// agree, every disagreement being reported as an error of its own; on
// a disagreement the graph the pass built is printed.
func TestFormBorrows(t *testing.T) {
	const decls = "struct Lens { text ref string }\nfunc take(s string) int { return 0 }\nfunc give(s string) string { return s }\nfunc grown(xs mut []int) ref []int { return xs }\nfunc idx(s ref string) int { return 0 }\n" +
		"func pick<T>(n int, m ref map[string]T) (ref T)? { return None }\nenum E { A(items []int); B }\nfunc mk() E { return A([]int{1}) }\n" +
		"struct W { xs []int }\nfunc mkw() W { return W{xs: []int{1}} }\nfunc (w ref W) Items() ref []int { return ref w.xs }\n" +
		"struct B2 { xs []string }\nfunc (b ref B2) First() (ref string)? { return None }\nfunc mkb() B2 { return B2{xs: []string{\"a\"}} }\n" +
		"struct Two { a string; src ref string }\nstruct B3 { n int }\nfunc (b B3) Pick(s ref string) (ref string)? { return Some(s) }\nfunc mkb3() B3 { return B3{n: 1} }\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f() { s := \"a\"; a := ref s; { t := \"b\".Clone(); a = ref t; a = a }; print(a) }", []string{"cannot keep a borrow of t in a: t is dropped at the end of its block"}},
		{"func f(s string) { var _, x = Lens{text: ref s}, take(s); print(x) }", []string{"cannot move s: s is borrowed at"}},
		{"func f(m mut map[string]int) { v := m.Get(\"a\"); m.Insert(\"b\", 1); print(v) }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f(s string) { xs := []int{0}; xs[idx(ref s)] = take(s); print(xs) }", nil},
		{"func f(s string) { var r, x = ref s, give(s); print(read(r), x) }", []string{"cannot move s: s is borrowed at"}},
		{"func f(xs []int) { var r, q ref []int = xs, grown(mut xs); print(r, q) }", []string{"cannot borrow xs as mut: xs is borrowed as ref at"}},
		{"func f() { m := map[string]string{}; if v := pick(0, ref m) { m.Remove(\"a\"); print(v) } }", []string{"cannot borrow m as mut: m is borrowed as ref by v"}},
		{"func f() int { z := []int{7}; r := ref z[0]; match ref mk() { A(items) => { r = ref items[0] }, _ => {} }; return *r }", []string{"cannot keep a borrow of mk() in r: mk() is dropped at the end of the statement that begins at"}},
		{"func f() int { z := 7; r := ref z; for _, x := range mkw().Items() { r = x }; return *r }", []string{"cannot keep a borrow of mkw() in r: mkw() is dropped at the end of the statement that begins at"}},
		{"func f() { z := \"z\"; r := ref z; if match mkb().First() { Some(v) => { r = v; true }, None => false } { print(r) } }", []string{"cannot keep a borrow of mkb() in r: mkb() is dropped after the condition that begins at"}},
		{"func f() { g := func(h mut Two) { h.src = ref h.a }; _ = g }", []string{"cannot store a borrow of h.a in h: h is the caller's"}},
		{"func f(t string) { g := func(s ref string) ref string { return s }; r := g(ref t); keep(t); print(r) }", []string{"cannot move t: t is borrowed by r"}},
		{"func f() int { outer := func() int { t := \"b\".Clone(); k := func() int { return len(t) }; inner := func() func() int { return k }; return inner()() }; return outer() }", []string{"closure k captures t by borrow and cannot be returned"}},
		{"func f(c bool, s string) { if c { return } else { return }; r := ref s; keep(s); print(r) }", nil},
		{"func f() { xs := []int{1, 2, 3}; print(xs[0:1], grow(mut xs)) }", []string{"cannot borrow xs as mut: xs is borrowed as ref at"}},
		{"func f(n int, s string) { r := ref s; for i := 0; i < 2; i += 1 { match n { 1 => print(1), _ => print(2) }; print(r); keep(s) } }", []string{"s moved in a previous iteration of the loop", "cannot move s: s is borrowed by r at 28:27, used in the next pass of the loop at"}},
		{"func f(s string) (ref string)? { if v := mkb3().Pick(ref s) { return Some(v) }; return None }", []string{"cannot return a borrow of local s"}}, // s is taken by value: it dies when f returns
		{"func f(s ref string) (ref string)? { if v := mkb3().Pick(s) { return Some(v) }; return None }", nil},
		// A binding through a borrow reads the borrow stored in the part, of the part's own type, or borrows the part, of a borrow of it.
		{"func f() string { var keep ref string = \"z\"; for x := range give(\"a,b\").Split(\",\") { keep = x }; return keep.Clone() }", []string{"cannot keep a borrow of give(...) in keep: give(...) is dropped at the end of the statement"}},
		{"func f(p ref string) ref string { for x := range p.Split(\",\") { return x }; return p }", nil},
		{"func f(p ref string) ref string { parts := p.Split(\",\"); for x := range ref parts { return *x }; return p }", nil},
		{"func f(p ref string) ref string { o := Some(p); r := ref o; return match r { Some(x) => x, None => p } }", nil},
		{"func f(p ref string) ref string { o := Some(p); return match ref o { Some(x) => x, None => p } }", nil},
		{"func f(p ref string) ref string { t := (p, 1); r := ref t; a, b := *r; return a }", nil},
		// A borrow a call takes holds what its place holds, which a later argument may not change.
		{"struct Row2 { name string }\nstruct L2 { r ref Row2; n int }\nfunc change(r mut Row2) int { r.name = \"z\".Clone(); return 0 }\nfunc show(r ref Row2, n int) { print(r.name) }\nfunc f() { row := Row2{name: \"a\".Clone()}; l := L2{r: ref row, n: 1}; show(l.r, change(mut row)) }", []string{"cannot borrow row as mut: row is borrowed as ref"}},
		{"func f() string { var keep ref string = \"z\"; var other ref string = \"z\"; { s := \"m\".Clone(); x := ref s; keep, other = *x, \"q\" }; return keep.Clone() + other.Clone() }", []string{"cannot keep a borrow of s in keep: s is dropped at the end of its block"}},
		{"func f<T>(s string) T? { return match T { struct => Some(T{ for f := range fields(T) { [f]: match f.type { string => s, _ => f.default ?? return None } } }), _ => None } }", []string{"s moved in a previous iteration of the loop"}},
		{"func f<T>(v mut T, w T) { match T { enum => match v { for c := range variants(T) => { *v = w } }, _ => {} } }", []string{"cannot assign to *v: v is borrowed by the arm written for each variant"}},
		{"func f<T>(s string) T? { return match T { struct => Some(T{ for f := range fields(T) { [f]: match f.type { func() int => func() int { return len(s) }, _ => f.default ?? return None } } }), _ => None } }", []string{"closure captures s by borrow and cannot be stored", "a closure that borrows cannot be chosen by if or match"}},
	}
	var graphs []string
	formGraphs = func(m *formBorrows) {
		var b strings.Builder
		b.WriteString(m.checker.flow.String())
		for i, t := range m.form.Temps {
			if t.Expr != nil {
				b.WriteString("  t" + strconv.Itoa(i) + " = " + exprText(t.Expr) + " origin t" + strconv.Itoa(t.Origin))
				if t.Bound {
					b.WriteString(" bound")
				}

				if t.Loop {
					b.WriteString(" loop")
				}

				b.WriteString("\n")
			}
		}

		graphs = append(graphs, b.String())
	}
	defer func() { formGraphs = nil }()
	for _, tt := range tests {
		graphs = graphs[:0]
		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q\n%s", tt.src, len(got), got, len(tt.want), tt.want, strings.Join(graphs, "\n"))
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
