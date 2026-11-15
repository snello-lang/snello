// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// TestTypeFuncs checks functions of a type: declared on a type of the
// package and called on it, on a generic type's instance and on a type
// parameter bounded by a trait that requires one; and the mistakes,
// each refused with its message.
func TestTypeFuncs(t *testing.T) {
	ok := []string{
		"struct P { x int }\nfunc P.Of(x int) P { return P{x: x} }\nfunc f() int { return P.Of(1).x }",
		"struct G<A> { a A }\nfunc G<A>.Of(a A) G<A> { return G<A>{a: a} }\nfunc f() int { return G<int>.Of(1).a }",
		"trait Mk { func Make() Self }\nstruct M impl Mk { n int }\nfunc M.Make() M { return M{n: 1} }\nfunc mk<T: Mk>() T { return T.Make() }\nfunc f() int { return mk<M>().n }",
		"struct P { x int }\nfunc P.Of(x int) P { return P{x: x} }\nfunc f() P { g := P.Of; return g(2) }",
	}
	for _, src := range ok {
		if got := checkErrors(t, "package p\n"+src); len(got) != 0 {
			t.Errorf("%s:\n  got %q, want no errors", src, got)
		}
	}

	bad := []struct {
		src  string
		want string
	}{
		{"struct P { x int }\nfunc P.Of(x int) P { return P{x: x} }\nfunc f(p P) P { return p.Of(1) }", "Of is a function of P, which takes no value of it: call P.Of(...)"},
		{"struct P { x int }\nfunc (p ref P) X() int { return p.x }\nfunc f() int { return P.X() }", "X is a method of P: call it on a value of it"},
		{"struct P { x int }\nfunc f() int { return P.Nope() }", "P has no function Nope"},
		{"struct P { x int }\nfunc (p ref P) Of() int { return 1 }\nfunc P.Of() P { return P{x: 1} }", "P.Of redeclared"},
		{"func int.Of() int { return 1 }", "a function of a type is declared on a declared type"},
		{"trait Show { func (s ref Self) Show() string }\nfunc Show.Default() int { return 1 }", "Show is a trait: it lists the functions it requires, func Default(...), and each type that implements it declares its own"},
		{"struct G<A> { a A }\nfunc G<int>.Of() int { return 1 }", "a function of a type names the type's parameters: G<A>"},
		{"func Result<T, E>.Make() int { return 1 }", "cannot define new functions on non-local type Result"},
		{"trait It<T> { func (s ref Self) Get() T }\nfunc It<T>.Make() int { return 1 }", "It is a trait: it lists the functions it requires, func Make(...), and each type that implements it declares its own"},
		// A function of a type takes no value: not through a trait value, a bounded parameter's value, or a trait as its own bound.
		{"trait Named { func (s ref Self) Name() string; func Count() int }\nfunc show(x ref Named) int { return x.Count() }", "cannot call Count through Named: it is a function of the type, which takes no value"},
		{"trait Mk { func Make() Self }\nfunc f<T: Mk>(v ref T) T { return v.Make() }", "Make is a function of T, which takes no value of it: call T.Make(...)"},
		{"trait K { func K() string }\nfunc via<T: K>() string { return T.K() }\nfunc f() string { return via<K>() }", "the function K of K takes no value, and a trait value has no type to call it on"},
		// A generic type's function is called on an instance, and a generic function is called, not taken as a value bare.
		{"struct G<A> { a A }\nfunc G<A>.Count() int { return 3 }\nfunc f() int { return G.Count() }", "G is generic: name an instance, G<...>.Count"},
		{"struct P { x int }\nfunc P.Twice<T>(v T) T { return v }\nfunc f() { g := P.Twice }", "cannot use the generic function Twice without calling it"},
		{"trait T { func Point.F() int }", "a trait's function is of the type that implements it"},
		{"enum E { A; B }\nfunc f() E { return E.A() }", "E has no function A: A is a variant of it, written by its name, A"},
		// A function's own type parameter would hide the one its type names.
		{"struct G<A> { a A }\nfunc G<A>.Of<A>(x A) A { return x }", "A is already a type parameter of G: name Of's own another way"},
		{"struct G<A> { a A }\nfunc (g ref G<A>) M<A>(x A) A { return x }", "A is already a type parameter of G: name M's own another way"},
	}
	for _, tt := range bad {
		got := checkErrors(t, "package p\n"+tt.src)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), tt.want) {
			t.Errorf("%s:\n  got %q\n  want an error containing %q", tt.src, got, tt.want)
		}
	}

	// One fault, one error: a generic type named without its parameters,
	// a function's type parameter that hides its type's, which is still
	// called on its type, and a field read on a type that is not one.
	for _, src := range []string{
		"struct G<A, B> { a A }\nfunc G.Of() int { return 1 }",
		"struct G<A> { a A }\nfunc G<A>.Of<A>(x A) G<int> { return G<int>{a: 1} }\nfunc f() int { return G<int>.Of<string>(\"s\").a }",
		"func f(x ref Nope) int { return x.y }",
		"trait It<T> { func (s ref Self) Get() T }\nfunc It<T>.Make() int { return 1 }",
		"func Clone.Make() int { return 1 }",        // a trait of the language
		"func Result<T, E>.Make() int { return 1 }", // a type of the language
	} {
		if got := checkErrors(t, "package p\n"+src+"\n"); len(got) != 1 {
			t.Errorf("%s:\n  got %d errors %q, want 1", src, len(got), got)
		}
	}
}

// TestTypeFuncPackages checks a function of another package's type: an
// exported one is called, an unexported one is refused, as a method of
// the type would be.
func TestTypeFuncPackages(t *testing.T) {
	lib := "package lib\nstruct Point { X int }\nfunc Point.Of(x int) Point { return Point{X: x} }\nfunc Point.secret() int { return 1 }\n"
	for _, tt := range []struct {
		src  string
		want string
	}{
		{"package main\nimport \"lib\"\nfunc f() int { return lib.Point.Of(1).X }\n", ""},
		{"package main\nimport \"lib\"\nfunc f() int { return lib.Point.secret() }\n", "secret"},
	} {
		info := &Info{}
		imp := &sourceImporter{srcs: map[string]string{"lib": lib}, loaded: map[string]*Package{}, info: info}
		imp.conf.Importer = imp
		f, file, err := parser.ParseFile("main.snello", []byte(tt.src))
		if err != nil {
			t.Fatal(err)
		}

		_, err = imp.conf.Check("", file, f, info)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: got %v, want no error", tt.src, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), "not exported")):
			t.Errorf("%s: got %v, want the unexported function refused", tt.src, err)
		}
	}
}
