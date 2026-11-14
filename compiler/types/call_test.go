// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

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
