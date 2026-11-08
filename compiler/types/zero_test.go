// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestZeroRefusals checks every way of making a zero value, refused for
// a type that has none with what to write instead, and allowed for one
// that has one; and replace, which leaves a value in the place for
// every type.
func TestZeroRefusals(t *testing.T) {
	const decls = `
struct Box { p *int; n int }
struct Owner impl Drop { n int }
func (o mut Owner) Drop() {}
func mk() Owner { return Owner{n: 1} }
`
	tests := []struct {
		src  string
		want []string
	}{
		// A declaration with no value, local and at the package level.
		{"func f() { var p *int; print(p) }", []string{"cannot declare p without a value: *int has no zero value; give it one, or declare it *int?"}},
		{"func f() { var o Owner; print(o) }", []string{"cannot declare o without a value: Owner has no zero value; give it one, or declare it Owner?"}},
		{"var g *int\nfunc f() {}", []string{"cannot declare g without a value: *int has no zero value"}},
		{"func f() { var c chan int; print(c) }", []string{"cannot declare c without a value: chan int has no zero value; give it one, or declare it (chan int)?"}},
		{"func f() { var g func(); print(g) }", []string{"cannot declare g without a value: func() has no zero value; give it one, or declare it (func())?"}},
		{"func f() { var xs []*int; var o Owner?; print(xs, o) }", nil},
		{"func f() { n := 1; var p *int = &n; print(p) }", nil}, // a value given
		// A struct literal.
		{"func f() { b := Box{n: 1}; print(b) }", []string{"missing field p in a Box literal: *int has no zero value; name it"}},
		{"func f() { n := 1; b := Box{p: &n}; print(b) }", nil}, // n has a zero value, left out
		// An array literal.
		{"func f() { n := 1; a := [2]*int{&n}; print(a) }", []string{"too few elements for [2]*int: 1 of 2, and *int has no zero value for the rest"}},
		{"func f() { a := [3]int{1}; print(a) }", nil},
		// make.
		{"func f(n int) { xs := make([]*int, n); print(xs) }", []string{"make cannot fill a []*int: *int has no zero value; use make([]*int, 0, n) and Push"}},
		{"func f(n int) { xs := make([]*int, 0, n); ys := make([]*int, 0); print(xs, ys) }", nil},
		{"func f(n int) { xs := make([]int, n); print(xs) }", nil},
		// A map read, and a write.
		{"func f(m map[string]*int) { print(m[\"a\"]) }", []string{"cannot read m[\"a\"]: *int has no zero value for a key the map does not hold; use m.Get(\"a\")"}},
		{"func f(m mut map[string]Owner) { m[\"a\"] = mk() }", nil},
		{"func f(m map[string]Owner) { if o := m.Get(\"a\") { print(o.n) } }", nil},
		// Generic code: a type parameter has no zero value.
		{"func g<T>() { var x T; print(x) }", []string{"cannot declare x without a value: T has no zero value"}},
		{"func g<T>(n int) []T { return make([]T, n) }", []string{"make cannot fill a []T: T has no zero value"}},
		{"func g<T>() { var x T?; print(x) }", nil},
		// A write through a map entry fills only part of the entry a missing key makes.
		{"func f(m mut map[string]Box) { m[\"a\"].n = 5 }", []string{"cannot write through m[\"a\"]: Box has no zero value for a key the map does not hold; use m.GetMut(\"a\")"}},
		{"func f(m mut map[string]Box) { m[\"c\"].n, m[\"d\"].n = 1, 2 }", []string{"cannot write through m[\"c\"]", "cannot write through m[\"d\"]"}},
		{"func f(m mut map[string][2]*int, p *int) { m[\"a\"][0] = p }", []string{"cannot write through m[\"a\"]: [2]*int has no zero value"}},
		{"func f(m mut map[string]Box, n int) { if b := m.GetMut(\"a\") { b.n = n } }", nil},
		{"func f(m mut map[string][]int) { m[\"a\"].Push(1) }", nil}, // a slice has a zero value, empty
		// A conversion to or from a type with no zero value.
		{"type Mine Owner\nfunc f(m Mine) Owner { return Owner(m) }", []string{"cannot convert Mine to Owner: Owner has no zero value"}},
		{"type Mine Owner\nfunc f(o Owner) Mine { return Mine(o) }", []string{"cannot convert Owner to Mine: Owner has no zero value"}},
		{"type Celsius int\nfunc f(n int) Celsius { return Celsius(n) }", nil},
		// A type that contains itself is reported as such, once, wherever its zero is asked for.
		{"struct S { t (int, S) }\nfunc f() { var s S; print(s) }", []string{"invalid recursive type S: it contains itself"}},
		{"enum E { A(xs [2]E); B }\nfunc f(m map[string]E) { print(m[\"a\"]) }", []string{"invalid recursive type E: it contains itself"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+tt.src)
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

// TestZeroAcrossPackages checks that another package's type without a
// zero value cannot be built with a literal that leaves out a field the
// program cannot name: sync.Mutex{} would be a lock with nothing behind
// it.
func TestZeroAcrossPackages(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"func f() { var m sync.Mutex<int>; print(m) }", []string{"cannot declare m without a value: sync.Mutex<int> has no zero value"}},
		{"func f() { m := sync.Mutex<int>{}; print(m) }", []string{"cannot build a sync.Mutex<int> with a literal: it has no zero value, and its field p is not exported; use its package's functions"}},
		{"func f() { var w sync.WaitGroup; print(w) }", []string{"cannot declare w without a value: sync.WaitGroup has no zero value"}}, // it holds an Arc
		{"func f() { var o sync.Once; print(o) }", []string{"cannot declare o without a value: sync.Once has no zero value"}},           // it holds a Mutex
		{"func f() { var r rc.Rc<int>; print(r) }", []string{"cannot declare r without a value: rc.Rc<int> has no zero value"}},         // it implements Drop
		{"func f() { var w rc.Weak<int>?; print(w) }", nil}, // an optional handle starts as None
		{"func f() { m := sync.NewMutex(1); print(m) }", nil},
		{"func f() { var b strings.Builder; b.WriteString(\"a\"); print(b.String()) }", nil}, // a zero value of its own
		// A type declared over another package's struct opens none of its fields, and does not convert to it.
		{"type MyC sync.Counter\nfunc f(m MyC) int { return m.h }", []string{"h of MyC is not exported"}},
		{"type MyC sync.Counter\nfunc f() MyC { return MyC{h: 5} }", []string{"h of MyC is not exported"}},
		{"type MyC sync.Counter\nfunc f(m MyC) sync.Counter { return sync.Counter(m) }", []string{"cannot convert MyC to sync.Counter: sync.Counter has no zero value"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, "package p\nimport (\"rc\"; \"strings\"; \"sync\")\n"+tt.src)
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
