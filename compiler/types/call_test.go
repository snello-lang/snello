// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// A conversion between a declared type and its underlying type gives
// the value itself under the other name: it moves a value that is not
// Copy, as an argument by value does, and takes no borrow of one, which
// would leave two owners to free it; a Copy value, a number and the
// conversions to a string's bytes or runes copy. A struct without a
// zero value only for its defaults converts, one that implements Drop
// does not.
func TestConversionMoves(t *testing.T) {
	const decls = "type Name string\ntype Ints []int\nstruct P { x int; s string }\ntype P2 P\nstruct D { x int; y int = 1 }\ntype D2 D\nstruct H { n Name }\ntype Celsius f64\n"
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(n Name) string { return string(n) }", nil},
		{"func f(n Name) string { s := string(n); print(n); return s }", []string{"use of moved value n"}},
		{"func f(xs Ints) { ys := []int(xs); print(xs, ys) }", []string{"use of moved value xs"}},
		{"func f(p P) P2 { q := P2(p); print(p.x); return q }", []string{"use of moved value p"}},
		{"func f(h H) string { s := string(h.n); print(h.n); return s }", []string{"use of moved value h.n"}},
		{"func f(ns []Name) string { return string(ns[0]) }", []string{"cannot move ns[0] out of the slice"}},
		{"func f(n ref Name) string { return string(n) }", []string{"cannot use a value of type ref string as string in return"}}, // a borrow converted is a view, which owns nothing to give
		{"func f(n mut Name) string { return string(n) }", []string{"cannot use a value of type mut string as string in return"}},
		{"func f(xs ref Ints) []int { return []int(xs) }", []string{"cannot use a value of type ref []int as []int in return"}},
		{"func f(n ref Name) ref string { return string(n) }", nil},                                                                     // read as its underlying type, by anyone
		{"func f(xs mut Ints) { ys := []int(xs); ys[0] = 1 }", nil},                                                                     // a mut view, in the package that declares Ints
		{"func f(s ref string) ref Name { return Name(s) }", nil},                                                                       // seen as Name, in the package that declares it
		{"func f(xs ref Ints) int { return len([]int(xs[1:])) + count(ref xs) }\nfunc count(xs ref []int) int { return len(xs) }", nil}, // a view of a view, and a ref passed as one of its underlying type
		{"func f(n Name) bool { return n == \"a\" && n[1:] == \"b\" }", nil},                                                            // a literal takes the declared type its context has
		{"func f(n ref Name) []byte { return []byte(n) }", nil},                                                                         // a string's bytes are copied
		{"func f(c ref Celsius) f64 { x := f64(c); print(c); return x }", nil},                                                          // Copy: read, not moved
		{"func f() D { return D(D2{x: 1}) }", nil},                                                                                      // no zero value only for the defaults
	}
	for _, tt := range tests {
		got := checkErrors(t, "package p\n"+decls+tt.src)
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

// TestViewsAcrossPackages checks the borrows another package converts:
// reading a declared type as its underlying type, a ref, is anyone's,
// and passes with nothing written where the underlying type has no
// name; seeing a value as a declared type, or a mut view of one, is the
// declaring package's alone.
func TestViewsAcrossPackages(t *testing.T) {
	lib := "package lib\ntype Sorted []int\ntype Name string\nfunc Own(xs mut []int) int { s := Sorted(xs); v := []int(s); v[0] += 1; return len(v) }\n"
	tests := []struct {
		src  string
		want string
	}{
		{"func f(s ref lib.Sorted) int { return sum([]int(s)) + sum(s) + sum(s[1:]) }", ""},
		{"func f(n ref lib.Name) ref string { return string(n[1:]) }", ""},
		{"func f(xs ref []int) int { s := lib.Sorted(xs); return len(s) }", "cannot convert ref []int to lib.Sorted: only package lib, which declares Sorted, may see a value as one"},
		{"func f(s ref string) int { n := lib.Name(s); return len(n) }", "cannot convert ref string to lib.Name: only package lib, which declares Name, may see a value as one"},
		{"func f(s mut lib.Sorted) { bump([]int(s)) }", "cannot convert mut lib.Sorted to []int: a mut view changes Sorted where its methods do not see it; only package lib, which declares it, may"},
		{"func f(s mut lib.Sorted) { bump(s) }", "cannot use s (mut lib.Sorted) as mut []int in argument"},
		{"func f(xs ref []int) { var s ref lib.Sorted = xs; print(len(s)) }", "cannot use xs (ref []int) as ref lib.Sorted in declaration"},
	}
	for _, tt := range tests {
		imp := &sourceImporter{srcs: map[string]string{"lib": lib}, loaded: map[string]*Package{}}
		imp.conf.Importer = imp
		src := "package main\nimport \"lib\"\nfunc sum(xs ref []int) int { return len(xs) }\nfunc bump(xs mut []int) { xs[0] += 1 }\n" + tt.src
		f, file, err := parser.ParseFile("test.snello", []byte(src))
		if err != nil {
			t.Fatal(err)
		}

		_, err = imp.conf.Check("", file, f, nil)
		got := ""
		if err != nil {
			got = err.Error()
		}

		if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got, tt.want)
		}
	}
}

// The conversions to a string: from its bytes, from a rune, a character
// literal typed as a rune, and from a []rune, borrowed operands too; an
// int is not a rune, so string(65) is refused rather than meaning "A".
func TestStringConversions(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(r rune) string { return string(r) }", nil},
		{"func f() string { return string('é') }", nil},
		{"func f(rs []rune) string { return string(rs) }", nil},
		{"func f(rs ref []rune) string { return string(rs) }", nil},
		{"func f(b ref []byte) string { return string(b) }", nil},
		{"func f(s ref string) []byte { return []byte(s) }", nil},
		{"func f(n int) string { return string(n) }", []string{"cannot convert int to string"}},
		{"func f() string { return string(65) }", []string{"cannot convert"}},
		{"func f(xs []int) string { return string(xs) }", []string{"cannot convert []int to string"}},
		{"func f(s string) []rune { return []rune(s) }", []string{"cannot convert string to []rune"}}, // a string's runes are s.Runes()
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

// replace moves a value out of a place and leaves another, for every
// type; take, only for a type with a zero value. Both need a place: a
// call's result, a constant or a byte of a string is a value made for
// the borrow, and what is moved out of it, or into it, would be lost.
func TestReplaceAndTake(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f(n mut Node) { m := Node{value: \"b\", next: None, n: 0}; old := replace(mut n.next, Some(&m)); print(old) }", nil},
		{"func f() { s := \"a\"; t := replace(mut s, \"b\"); keep(t); keep(s) }", nil}, // the place holds a value after
		{"func f() { s := \"a\"; t := \"b\"; u := replace(mut s, t); keep(u); keep(t) }", []string{"use of moved value t"}},
		{"func f(xs mut []string) { old := replace(mut xs[0], \"z\"); keep(old) }", nil},
		{"func f(n ref Node) { old := replace(mut n.value, \"z\"); keep(old) }", []string{"cannot borrow n.value as mut through a ref borrow"}},
		{"func f(n mut Node) { old := replace(n.value, \"z\"); keep(old) }", []string{"replace moves a value out of a place written mut: replace(mut x.f, v)"}},
		{"func f(n mut Node) { old := replace(mut n.n); print(old) }", []string{"replace takes a place and a value: replace(mut place, v)"}},
		{"func f(n mut Node) { old := replace(mut n.n, \"a\"); print(old) }", []string{"cannot use"}},
		{"func g() string { return \"a\" }\nfunc f() { old := replace(mut g(), \"b\"); keep(old) }", []string{"replace needs a place, a variable or a field, element or *p of one: g() is not one"}},
		{"func f() { const c = 3; old := replace(mut c, 4); print(old) }", []string{"replace needs a place"}},
		{"func f(s string) { old := replace(mut s[0], 65); print(old) }", []string{"replace needs a place"}},
		{"func g() string { return \"a\" }\nfunc f() { old := take(mut g()); keep(old) }", []string{"take needs a place, a variable or a field, element or *p of one: g() is not one"}},
		{"func f(n mut Node) { x := take(mut n.next); print(x) }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}

// A program's own variadic parameters: the arguments checked one by one
// against the element, after ... the slice against the parameter; a
// generic element inferred from every argument; a variadic function as
// a value of its own type, which a function of a slice is not; a trait
// method variadic as the trait's is; and the type written as it is
// declared.
func TestVariadicCalls(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func sum(xs ...int) int { return len(xs) }\nfunc f(s []int) int { return sum() + sum(1, 2) + sum(s...) }", nil},
		{"func show(sep string, xs ...ref string) {}\nfunc f(a string, s []string) { show(\",\", ref a, a); show(\",\", s...); print(a, s) }", nil},
		{"func count<T>(xs ...T) int { return len(xs) }\nfunc f() int { return count(1, 2) + count(\"a\".Clone()) }", nil},
		{"func count<T>(xs ...T) int { return len(xs) }\nfunc f() int { return count(1, \"a\") }", []string{"cannot use"}},
		{"func count<T>(xs ...T) int { return len(xs) }\nfunc f() int { return count() }", []string{"cannot infer the type parameter T of count"}},
		{"func sum(xs ...int) int { return len(xs) }\nfunc f() { g := sum; print(g(1, 2)) }", nil},
		{"func sum(xs ...int) int { return len(xs) }\nfunc f() { var g func([]int) int = sum; print(g) }", []string{"cannot use"}},
		{"func f() { g := func(xs ...int) int { return len(xs) }; print(g(1, 2, 3)) }", nil},
		{"func sum(xs ...int) int { return len(xs) }\nfunc f() { print(sum(1, 2.5)) }", []string{"cannot use"}},
		{"trait Logger { func (l ref Self) Log(parts ...ref string) }\nstruct L impl Logger {}\nfunc (l ref L) Log(parts ...ref string) {}", nil},
		{"trait Logger { func (l ref Self) Log(parts ...ref string) }\nstruct L impl Logger {}\nfunc (l ref L) Log(parts []string) {}", []string{"method L.Log has the wrong signature for Logger: have (ref L) Log([]string), want (ref L) Log(...ref string)"}},
		{"enum E { Many(xs ...int) }", []string{"a variant's field is not variadic"}},
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

	for _, tt := range []struct{ sig, want string }{
		{"func(xs ...int) int", "func(...int) int"},
		{"func(sep string, xs ...ref string)", "func(string, ...ref string)"},
	} {
		got := checkErrors(t, prelude+"func f() { var g "+tt.sig+" = 1 }")
		if len(got) != 1 || !strings.Contains(got[0], tt.want) {
			t.Errorf("%s: got %q, want a message naming %s", tt.sig, got, tt.want)
		}
	}
}
