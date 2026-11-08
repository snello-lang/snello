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
