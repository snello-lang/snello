// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// The type a variadic parameter has inside its function, its form, and
// the mark a ...ref one carries: ...T is an owned []T, ...ref T a
// ref []T, lent for the call; a fixed parameter is neither.
func TestVariadicParams(t *testing.T) {
	src := "package p\nfunc owned(n int, xs ...string) {}\nfunc lent(xs ...ref []int) {}\nfunc fixed(xs []string) {}"
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := libraryConfig().Check("", file, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		fn, param string
		kind      VariadicKind
		elem      string
		lent      bool
	}{
		{"owned", "[]string", VariadicOwned, "string", false},
		{"lent", "ref [][]int", VariadicLent, "[]int", true},
		{"fixed", "[]string", NotVariadic, "", false},
	} {
		sig := pkg.Scope().Lookup(tt.fn).Type().(*Signature)
		last := sig.Params()[len(sig.Params())-1]
		kind, elem := sig.VariadicParam()
		got := ""
		if elem != nil {
			got = elem.String()
		}

		if last.Type().String() != tt.param || kind != tt.kind || got != tt.elem || last.lent != tt.lent {
			t.Errorf("%s: parameter %s, kind %d, element %q, lent %v; want %s, %d, %q, %v", tt.fn, last.Type(), kind, got, last.lent, tt.param, tt.kind, tt.elem, tt.lent)
		}
	}
}

// The variadic parameters refused: ...mut T, a program's ...ref any,
// one of a type that holds a borrow, and a variant's field; and the
// lengths of arrays that are no constant.
func TestTypeExprErrors(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want string
	}{
		{"func f(xs ...mut int) {}", "a variadic parameter is ...T or ...ref T, not ...mut T"},
		{"func f(xs ...ref any) {}", "...ref any is the library's printing functions' alone"},
		{"func f(xs ...ref ref int) {}", "a variadic parameter cannot hold a borrow"}, // the outer ref is the variadic's own; the element ref int is the borrow a slice cannot hold
		{"func f(x mut ref int) {}", "cannot write mut of a borrow type: a borrow is not borrowed mut; write mut of what it borrows, or ref of it, which is the borrow itself"},
		{"func f(x mut mut int) {}", "cannot write mut of a borrow type"},
		{"enum E { Many(xs ...int) }", "a variant's field is not variadic"},
		{"func f(n int) { var a [n]int; print(a) }", "array length must be a non-negative integer constant: n is not constant"},
	} {
		got := checkErrors(t, "package p\n"+tt.src)
		if len(got) != 1 || !strings.Contains(got[0], tt.want) {
			t.Errorf("%s: got %q, want one error containing %q", tt.src, got, tt.want)
		}
	}
}
