// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// lentText writes what the value o lends, as the form follows it: the
// temporary a borrow in it is of, or the places it lends, each lent or
// read.
func lentText(m *formBorrows, o Operand) string {
	if tmp := m.lends(o); tmp != NoTemp {
		return "temporary " + exprText(m.form.Temps[tmp].Expr)
	}

	var parts []string
	for _, lp := range m.lent {
		how := "read "
		if lp.lent {
			how = "lent "
		}

		parts = append(parts, how+m.form.OperandString(lp.place))
	}

	if len(parts) == 0 {
		return "nothing"
	}

	return strings.Join(parts, ", ")
}

// formLends runs the checker over src and returns, for each return, each
// binding a pattern takes from a value made here and each argument of a
// deferred call in the bodies it checks, what the form says the value
// lends, one line each.
func formLends(t *testing.T, src string) []string {
	t.Helper()
	var lines []string
	formGraphs = func(m *formBorrows) {
		f := m.form
		for _, b := range f.order {
			for i := f.Blocks[b].From; i < f.Blocks[b].To; i++ {
				in := &f.Instrs[i]
				switch {
				case in.Op == OpReturn && len(in.Args) == 1 && in.Expr != nil:
					lines = append(lines, "return "+exprText(in.Expr)+": "+lentText(m, in.Args[0]))
				case in.Op == OpStore && !in.Place.IsTemp() && in.To.IsValue() && in.To.Path != "":
					var parts []string
					m.partLends(in.To)
					for _, lp := range m.lent {
						parts = append(parts, m.form.OperandString(lp.place))
					}
					lines = append(lines, "bind "+f.OperandString(in.Place)+": "+strings.Join(parts, ", "))
				case in.Op == OpDefer:
					for _, a := range in.Args {
						if part := m.partOfTemporary(a); part != nil {
							lines = append(lines, "defer drops "+exprText(part))
						}
					}
				}
			}
		}
	}
	defer func() { formGraphs = nil }()
	checkErrors(t, src)
	return lines
}

// TestLends follows what a value lends through the instructions that
// make it: a borrow of a place lends it, a borrow variable lent on
// passes on what it holds, a part of a value made here borrows the
// value, a literal is no temporary, a call's result lends what its
// source does, a value chosen by an if, a match or a ?? lends what each
// arm does, and a pattern binds what the element it takes lends. The
// body checked last is the test's, whose last return, or the binding
// named, is read.
func TestLends(t *testing.T) {
	const decls = "struct Span { text ref string; at int }\nenum H { Holder(ref string); Empty }\nfunc mk() string { return \"m\".Clone() }\nfunc mkInts() []int { return []int{1} }\n" +
		"func id(x ref string) ref string { return x }\nfunc pick<T>(a T, b T) T { return a }\nstruct Box { v string }\nfunc (b ref Box) Get() ref string { return ref b.v }\nfunc mkBox() Box { return Box{v: \"b\".Clone()} }\n"
	tests := []struct {
		src  string
		what string // the last return, or bind and a binding's name
		want string
	}{
		{"func f(p ref string) ref string { return p }", "return", "read p"},
		{"func f(p ref string) ref string { return p[0:2] }", "return", "read p"},
		{"func f() ref string { s := \"a\".Clone(); return ref s }", "return", "lent s"},
		{"func f() ref string { s := \"a\".Clone(); return s }", "return", "lent s"}, // given where a borrow is wanted
		{"func f(t ref string) ref string { x := Span{text: t, at: 1}; return id(x.text) }", "return", "read x.text"},
		{"func f() ref string { return id(mk()) }", "return", "temporary mk()"},
		{"func f() ref string { return mk()[0:2] }", "return", "temporary mk()"},
		{"func f() ref string { return mkBox().Get() }", "return", "temporary mkBox()"},
		{"func f(c bool) ref string { return if c { \"a\" } else { \"b\" } }", "return", "nothing"},  // string literals: data of the program
		{"func f(c bool) ref int { return ref (if c { 1 } else { 2 }) }", "return", "temporary ..."}, // a number chosen is copied into the if's value
		{"func f(c bool, d bool) ref string { return if c { \"a\" } else if d { \"b\" } else { \"c\" } }", "return", "nothing"},
		{"func f(c bool, p ref string) ref string { return if c { p } else { \"x\" } }", "return", "read p"},
		{"func f(p ref string) ref string { o := Some(p); return o ?? \"z\" }", "return", "read o"},
		{"func f(p ref string) ref string { return pick<ref string>(mk(), p) }", "return", "temporary mk()"}, // a T given a value made here, lent
		{"func f(p ref string) H { return Holder(p) }", "return", "read p"},
		{"func f() H { s := \"a\".Clone(); return Holder(s) }", "return", "lent s"}, // a ref field given an owned variable lends it
		{"func f() (ref string)? { s := \"a\".Clone(); return Some(s) }", "return", "lent s"},
		{"func f() ref int { return ref 5 }", "return", "nothing"},
		{"func f() ref int { return ref mkInts()[0] }", "return", "temporary mkInts()"},
		{"func f(s mut []int) mut []int { return mut s[1:] }", "return", "temporary s[1:]"}, // a mut view borrows the header its slice expression makes
		{"func f(p ref string) ref string { a, b := (p, ref mk()); print(b); return a }", "bind a", "p"},
		{"func f(p ref string) ref string { a, b := (p, ref mk()); print(b); return a }", "bind b", "t0"}, // the temporary mk() is
		{"func f(p ref string) ref string { match Holder(p) { Holder(x) => return x, _ => {} }; return p }", "bind x", "p"},
	}
	for _, tt := range tests {
		lines := formLends(t, prelude+decls+tt.src)
		got, found := "", false
		for _, l := range lines {
			if strings.HasPrefix(l, tt.what+" ") || strings.HasPrefix(l, tt.what+":") {
				got, found = l[strings.LastIndex(l, ": ")+2:], true
			}
		}

		if !found || got != tt.want {
			t.Errorf("%s: %s lends %q, want %q", tt.src, tt.what, got, tt.want)
		}
	}
}

// TestPartOfTemporary refuses an argument a deferred call would read
// after the defer statement drops what it borrows: a part, a view and
// what a call's result borrows of a value made here, but not the whole
// value made for the call, which the call keeps.
func TestPartOfTemporary(t *testing.T) {
	const decls = "struct Row { name string }\nfunc row() Row { return Row{name: \"r\".Clone()} }\nfunc mk() string { return \"m\".Clone() }\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f() { defer fmt.Println(row().name) }", []string{"after the defer statement drops row()"}},
		{"func f() { defer fmt.Println(mk()[0:1]) }", []string{"after the defer statement drops mk()"}},
		{"func f() { defer fmt.Println(mk().Trim()) }", []string{"after the defer statement drops mk()"}},
		{"func f() { defer fmt.Println(mk()) }", nil},
		{"func f() { r := row(); defer fmt.Println(r.name) }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %q\n  want %q", tt.src, got, tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// TestPartType reads the type of a part a pattern or an element names
// off the type of the whole: through a borrow, an element, a field, a
// tuple's element and a variant's field, an option's and an enum's;
// nil for a part the type does not have.
func TestPartType(t *testing.T) {
	str := Typ[String]
	view := NewRef(str, false)
	tests := []struct {
		t    Type
		path string
		want Type
	}{
		{NewSlice(view), "[]", view},
		{NewRef(NewSlice(view), false), "[]", view}, // a part of a borrow is the part of what it points at
		{NewTuple(view, Typ[Int]), "?0", view},
		{NewTuple(view, Typ[Int]), "?1", Typ[Int]},
		{NewOption(view), "?Some.?0", view},
		{NewRef(NewOption(view), false), "?Some.?0", view},
		{NewTuple(view, Typ[Int]), "?2", nil},
		{NewOption(view), "?Ok.?0", nil},
		{Typ[Int], "x", nil},
	}
	for _, tt := range tests {
		if got := partType(tt.t, tt.path); got != tt.want && !(got != nil && tt.want != nil && Identical(got, tt.want)) {
			t.Errorf("partType(%v, %q) = %v, want %v", tt.t, tt.path, got, tt.want)
		}
	}
}
