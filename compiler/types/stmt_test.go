// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// A range over a string: what its variables get, and that the string
// is lent for the loop, not consumed by it.
func TestRangeString(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(s string) { for i, r := range s { var a int = i; var b rune = r; print(a, b) }; print(s) }", nil}, // the offset, the rune, and s after the loop
		{"func f(s string) { for i := range s { var a int = i; print(a) } }", nil},
		{"func f(s ref string) { for _, r := range s { var b rune = r; print(b) } }", nil},
		{"func f(s string) { for _, r := range s[1:] { print(r) } }", nil},
		{"func f(s string) { for _, r := range s { var b byte = r; print(b) } }", []string{"cannot use r (rune) as byte in declaration"}},
		{"func f(s string) { for _, r := range s { s = \"x\"; print(r) } }", []string{"cannot assign to s: s is borrowed by the range loop"}},
		{"func f(s string) { for _, r := range s { print(r); keep(s) } }", []string{"s moved in a previous iteration of the loop", "cannot move s: s is borrowed by the range loop"}},
		{"func f(s string) { for _, r := range s { print(r); keep(s); break } }", nil}, // no pass comes round to read s again
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// TestBlankVarTyped gives the blank name of a local var declaration the
// type of its value, as any name declared with one is given, though no
// scope holds it: a tool that asks for the variable's type finds one.
func TestBlankVarTyped(t *testing.T) {
	src := "package p\nfunc main() {\n\tvar _ = 5\n\tvar _, b = \"x\", true\n\tprint(b)\n}\n"
	f, file, err := parser.ParseFile("blank.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	if _, err := Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	var blanks []string
	for ident, obj := range info.Defs {
		if ident.Name == "_" {
			if obj.Type() == nil {
				t.Fatalf("the blank variable at %v has no type", ident.NamePos)
			}

			blanks = append(blanks, obj.Type().String())
		}
	}

	slices.Sort(blanks)
	if want := []string{"int", "string"}; !slices.Equal(blanks, want) {
		t.Errorf("the blank variables' types: %v, want %v", blanks, want)
	}
}

// TestSeveralValues checks the values of a statement of several left to
// right, as a call's arguments are, in var, := and =: a later value can
// neither take nor change what an earlier one borrows, nor read what it
// moved; and a part of a tuple taken apart holds what the tuple lends.
// What one statement of each kind may do stays allowed.
func TestSeveralValues(t *testing.T) {
	const decls = "struct Lens { text ref string }\nstruct Count { n mut int }\n" +
		"func take(s string) string { return s }\nfunc give(c Count, m int) {}\nfunc first(s ref string) ref string { return s }\n" +
		"func grown(xs mut []int) ref []int { return xs }\nfunc made() Node { return Node{value: \"made\"} }\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(s string) { var r, x = ref s, take(s); print(read(r), x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(s string) { r, x := ref s, take(s); print(read(r), x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(s string, t string) { r := ref t; x := \"\"; r, x = ref s, take(s); print(read(r), x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(s string) { _, x := Lens{text: ref s}, take(s); print(x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(s string) { var _, x = Lens{text: ref s}, take(s); print(x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(s string) { var g, x = func() { print(read(s)) }, take(s); g(); print(x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(s string) { var r, x = first(ref s), take(s); print(read(r), x) }", []string{"cannot move s: s is borrowed"}},
		{"func f(k int) { var _, m = Count{n: mut k}, k; print(m) }", []string{"cannot use k: k is borrowed as mut"}},
		{"func f(s string) { a, c := s, read(s); print(a, c) }", []string{"use of moved value s"}},
		{"func f(s string) { var a, c = s, read(s); print(a, c) }", []string{"use of moved value s"}},
		{"func f(s string) { var r, n = (ref s, 1); keep(s); print(read(r), n) }", []string{"cannot move s: s is borrowed by r"}},
		{"func f(s string) { a := \"old\"; r := ref s; a, r = \"new\", ref a; print(read(r)) }", []string{"cannot assign to a: a is borrowed"}}, // a place a value borrows keeps its value to the statement's end
		{"func f(s string) { a := \"old\"; a, r := \"new\", ref a; print(read(r)) }", []string{"cannot assign to a: a is borrowed"}},
		{"func f(xs []int) { var r, q ref []int = xs, grown(mut xs); print(r, q) }", []string{"cannot borrow xs as mut: xs is borrowed as ref"}}, // a place given where a borrow is wanted is lent
		{"var base = made()\nvar copied = base\nfunc f() { print(copied.value) }", []string{"base is a package variable"}},
		{"func f(s string, t string) { r := ref t; n := 0; r, n = s, 1; print(read(r), n, read(s)) }", nil}, // given to a ref place, s is lent, not moved
		{"func f(s string, t string) { r := ref t; r, n := s, 1; print(read(r), n, read(s)) }", nil},
		{"func f(a string, b string) { a, b = b, a; print(a, b) }", nil},
		{"func f(s string) { r, n := ref s, read(s); print(read(r), n) }", nil},
		{"func f(s string) { var _, n = s, 1; print(n) }", nil},
		{"func f(s string) { var a, n int = s, read(s); print(a, n, read(s)) }", []string{"cannot use s (string) as int in declaration"}}, // one that does not fit moves nothing
		{"func f(k int) { give(Count{n: mut k}, 2) }", nil},                                                                               // a literal's mut lends once for the call
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// TestBlankGuard refuses the blank name given a value made where it
// stands whose Drop may read what it borrows, a lock's guard, which it
// would end as it begins, wherever the blank stands; and keeps the
// blank for a variable ended early, _ = g, for a value with a Drop that
// borrows nothing, for a borrow with no Drop, and for a borrow beside a
// Drop that holds none.
func TestBlankGuard(t *testing.T) {
	const decls = "struct Guard impl Drop { held ref string }\nfunc (g mut Guard) Drop() { print(g.held) }\n" +
		"struct Note impl Drop { name string }\nfunc (n mut Note) Drop() {}\nstruct Lens { text ref string }\n" +
		"struct Beside { text ref string; note Note }\n"
	const refused = "the blank name would drop this Guard at once"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(s string) { var _ = Guard{held: ref s} }", []string{refused}},
		{"func f(s string) { _ = Guard{held: ref s} }", []string{refused}},
		{"func f(s string) { _, n := Guard{held: ref s}, 1; print(n) }", []string{refused}},
		{"func f(s string) { n := 0; _, n = Guard{held: ref s}, 1; print(n) }", []string{refused}},
		{"func f(s string) { var _, n = (Guard{held: ref s}, 1); print(n) }", []string{refused}},
		{"func f(s string) { g := Guard{held: ref s}; _ = g; print(s) }", nil},
		{"func f() { var _ = Note{name: \"n\"}; _ = Note{name: \"m\"} }", nil},
		{"func f(s string) { var _ = Lens{text: ref s}; var _ = ref s }", nil},
		{"func f(s string) { _ = Beside{text: ref s, note: Note{name: \"n\"}}; _ = (ref s, Note{name: \"m\"}) }", nil}, // the Drop beside the borrow reads none
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// TestAssignOrder checks an assignment in the order it runs: the value,
// or every value, is made first, and the place is reached after it,
// where the store is made, so a value can neither end nor take what the
// place is reached through, with one value or several.
func TestAssignOrder(t *testing.T) {
	const decls = "func drained(v []string) string { return \"\" }\nfunc eaten(p *Node) Node { return Node{} }\n" +
		"func counted(p *Node) int { return 0 }\nfunc first(s mut string) mut string { return s }\nfunc taken(s string) int { return 0 }\n" +
		"func pairEat(t (mut int, int)) int { return 1 }\nfunc seen(s mut string) int { return 0 }\nfunc idx(s ref string) int { return 0 }\n" +
		"func headOf(n mut Node) mut Node { return n }\nstruct Pt { x f64; o int? }\nfunc getPt(p mut Pt) mut Pt { return p }\n" +
		"trait Tagged {\n\tvar tag int?\n\tfunc (t mut Self) Reset() { t.tag = None }\n}\nfunc clearTag<T: Tagged>(x mut T) { x.tag = None }\n" +
		"struct B { n int; name string }\nfunc pick(b mut B, k int) mut B { return b }\nfunc pickT(b mut B, s string) mut B { return b }\nfunc pick2(b mut B, k int, m int) mut B { return b }\nfunc pickf(b mut B, f func()) mut B { return b }\n" +
		"func (n mut Node) First() mut Node { return n }\nfunc countedN(n Node) int { return 0 }\nfunc rows(xs mut []int) mut []int { return xs }\nfunc takenXs(xs []int) int { return 0 }\nfunc takenB(b B) int { return 0 }\n" +
		"struct HI { r ref int }\nfunc pickHI(h mut HI, k int) mut HI { return h }\nfunc growI(v mut []int) int { return 0 }\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(v []string) { r := mut v[0]; *r = drained(v) }", []string{", used at "}}, // the place's use follows the value, though it stands before it, and is no loop's next pass
		{"func f(v []string) { r := mut v[0]; n := \"\"; *r, n = \"z\", drained(v); print(n) }", []string{"cannot move v: v is borrowed by r"}},
		{"func f(p *Node) { *p = eaten(p) }", []string{"use of moved value p"}},
		{"func f(p *Node) { n := 0; *p, n = Node{}, counted(p); print(n) }", []string{"use of moved value p"}},
		{"func f(s string) { n := 0; *first(mut s), n = \"z\", taken(s); print(n) }", []string{"use of moved value s"}},
		{"func f(v []string) { r := mut v[0]; *r = \"z\"; keep(v[0].Clone()) }", nil},
		{"func f(xs []int, i int) { xs[i], i = 7, 1; print(xs, i) }", nil},
		// An index is evaluated before the value, so a value that takes what it reads comes after the read; a key the map keeps is taken as it is read.
		{"func f(r mut int, ys []int) { ys[*r] = pairEat((r, 1)); print(ys) }", nil},
		{"func f(s string) { xs := []int{0}; xs[idx(ref s)] = taken(s); print(xs) }", nil},
		{"func f(k string) { m := map[string]int{}; m[k] = taken(k) }", []string{"use of moved value k"}},
		{"func f(k string) { m := map[string]int{}; m[k] = seen(mut k) }", []string{"use of moved value k"}},
		{"func f(k string) { m := map[string]int{}; n := 0; m[k], n = seen(mut k), 1; print(n) }", []string{"use of moved value k"}},
		// The place is typed before the value, so the value takes its type as a hint, through a call, a borrow and a trait's field too.
		{"func f(n Node) { headOf(mut n).next = None; print(n.n) }", nil},
		{"func f(p Pt) { getPt(mut p).x = 2; getPt(mut p).o = None; print(p.x) }", nil},
		{"func f(d Node) { d.o = None }", []string{"is not a field"}}, // and a place that is wrong is the one error
		// A call the place is reached through runs at the store: what its arguments and receiver read is read after the value.
		{"func f(s string) { b := B{n: 0, name: \"b\"}; pick(mut b, read(s)).n = taken(s) }", []string{"use of moved value s"}},
		{"func f(n Node) { n.First().n = countedN(n) }", []string{"use of moved value n"}},
		{"func f(xs []int) { rows(mut xs)[0] = takenXs(xs) }", []string{"use of moved value xs"}},
		{"func f(s string) { b := B{n: 0, name: \"b\"}; r := mut s; pick(mut b, len(s)).n = 1; print(seen(r)) }", []string{"cannot use s: s is borrowed as mut by r"}},
		{"func f(s string) { b := B{n: 0, name: \"b\"}; pick(mut b, len(s)).n = len(s); print(b.n) }", nil},
		{"func f(s string) { b := B{n: 0, name: \"b\"}; pickT(mut b, s).n = len(s); print(b.n) }", nil}, // the call's move is made at the store, after the value read s
		{"func f(s string) { b := B{n: 0, name: \"b\"}; pickT(mut b, s).n = taken(s) }", []string{"use of moved value s"}},
		// A closure in the place runs when it is called: its body is no part of the store, and leaves the place's check as it was.
		{"func f() { xs := []int{1}; b := B{n: 0, name: \"b\"}; pickf(mut b, func() { r := ref xs; xs.Push(1); print(r) }).n = 1 }", []string{"cannot borrow xs as mut"}},
		{"func f() { b := B{n: 0, name: \"b\"}; pickf(mut b, func() { j := 0; j = 1; print(j) }).n = takenB(b) }", []string{"use of moved value b"}},
		{"func f(s string, c bool) { b := B{n: 0, name: \"b\"}; j := 0; pick2(mut b, if c { j = 1; 2 } else { 3 }, read(s)).n = taken(s); print(j) }", []string{"use of moved value s"}}, // an assignment inside the place leaves its check as it was
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; j := 0; pick2(mut b, if c { j = 1; 2 } else { 3 }, 0).n = 1; print(j) }", nil},
		// Statements inside the place run at the store too: a loan, a drop or an assignment in an arm there is made after the value, in its order.
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; s2 := \"old\".Clone(); r := ref s2; pick2(mut b, if c { s2 = \"new\".Clone(); 2 } else { 3 }, 0).n = 1; print(read(r), b.n) }", []string{"cannot assign to s2: s2 is borrowed by r"}},
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; pick2(mut b, if c { s2 := \"a\".Clone(); r := ref s2; s2 = \"b\".Clone(); len(r) } else { 3 }, 0).n = 1 }", []string{"cannot assign to s2: s2 is borrowed by r"}},
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; s2 := \"old\".Clone(); r := ref s2; n := 0; pick2(mut b, if c { s2, n = \"new\".Clone(), 1; 2 } else { 3 }, 0).n = 1; print(read(r), n) }", []string{"cannot assign to s2: s2 is borrowed by r"}},
		{"func f(c bool) { b := B{n: 0, name: \"b\"}; pick2(mut b, if c { xs := []int{7}; r := ref xs[0]; growI(mut xs); print(*r); 0 } else { 1 }, 0).n = 1 }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r"}},
		{"func f(s string, c bool) { b := B{n: 0, name: \"b\"}; pick(mut b, if c { r := ref s; len(r) } else { 1 }).n = taken(s) }", []string{"use of moved value s"}},
		{"func f(c bool, v2 []string) { b := B{n: 0, name: \"b\"}; r2 := mut v2[0]; pick2(mut b, if c { *r2 = drained(v2); 2 } else { 3 }, 0).n = 1 }", []string{"cannot move v2: v2 is borrowed by r2"}}, // an assignment nested in the place keeps its own order, its value before its place
		// What one value lends stays lent past the store, as several values' does: the place's call cannot take it.
		{"func f() { v := []int{7}; w := 0; h := HI{r: ref w}; pickHI(mut h, growI(mut v)).r = ref v[0]; print(*h.r) }", []string{"cannot borrow v as mut: v is borrowed as ref"}},
		// A compound assignment reads its place, then reaches it again to store, after the value.
		{"func f(xs []int) { rows(mut xs)[0] += takenXs(xs) }", []string{"use of moved value xs"}},
		{"func f() { b := B{n: 0, name: \"b\"}; pick(mut b, 0).n += takenB(b) }", []string{"use of moved value b"}},
		{"func f(xs []int) { rows(mut xs)[0] += len(xs); print(xs) }", nil},
		// A path that leaves inside the place never reaches the store: what it drops or assigns is its own, and the path that goes on keeps its borrows.
		{"func f(o int?) { xs := []int{1}; r := ref xs; b := B{n: 0, name: \"b\"}; pick2(mut b, o ?? return, 0).n = 1; print(len(r), b.n) }", nil},
		{"func f(c bool) { xs := []int{1}; r := ref xs; b := B{n: 0, name: \"b\"}; pick2(mut b, if c { return } else { 3 }, 0).n = 1; print(len(r), b.n) }", nil},
		{"func f(o int?) { xs := []int{1}; r := ref xs; b := B{n: 0, name: \"b\"}; pick2(mut b, o ?? return, 0).n = 1; xs.Push(2); print(len(r)) }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r"}},
		{"func f(c bool, t string) { s := \"a\".Clone(); r := ref s; b := B{n: 0, name: \"b\"}; pick2(mut b, if c { r = ref t; return } else { 3 }, 0).n = 1; s = \"b\".Clone(); print(read(r)) }", []string{"cannot assign to s: s is borrowed by r"}},
		// A value's borrow of the place it is stored to is lent past the store, which writes what it borrows; and the value would hold a borrow of its own data.
		{"struct V { r ref string; own string }\nfunc f(a ref string) { v := V{r: a, own: \"b\".Clone()}; v = V{r: ref v.own, own: \"c\".Clone()}; print(read(v.r)) }", []string{"cannot assign to v: v.own is borrowed", "cannot store a borrow of v.own in v: a value cannot borrow its own data"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got %q\n  want it to contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
