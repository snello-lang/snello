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
		"struct Two { a string; src ref string }\n"
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
