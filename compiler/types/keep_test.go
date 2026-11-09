// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// TestKeeps checks which parameters a function keeps, from its body,
// and that a closure that borrows is refused for exactly those: each
// way a body keeps a parameter, a parameter passed on to a function
// that keeps it, wherever in the package that function is, and each
// way around the rule, a function used as a value, a closure, and a
// trait's method.
func TestKeeps(t *testing.T) {
	const helpers = "struct Keeper { f func() string }\n" +
		"struct Box<T> { v T? }\n" +
		"func store(k mut Keeper, f func() string) { k.f = f }\n" +
		"func call(f func() string) string { return f() }\n"
	const borrowing = "s := \"hello\".Clone(); r := ref s; "
	const closure = "func() string { return (*r).Clone() }"
	tests := []struct {
		src  string
		want []string
	}{
		// The issue's program: store keeps f, so a closure that borrows cannot be passed to it.
		{"func f() { k := Keeper{f: func() string { return \"\".Clone() }}; { " + borrowing + "store(mut k, " + closure + ") }; print(k.f()) }",
			[]string{"closure captures r by borrow and cannot be passed to store, which keeps f: assigned to k.f at"}},
		// A closure that borrows nothing may be kept; one that borrows may be passed where it is only called.
		{"func f() { k := Keeper{f: func() string { return \"\".Clone() }}; store(mut k, func() string { return \"a\".Clone() }); print(k.f()) }", nil},
		{"func f() { " + borrowing + "print(call(" + closure + ")) }", nil},
		{"func f(xs mut []int) { bias := \"ab\".Clone(); xs.SortBy(func(a ref int, b ref int) bool { return *a + bias.Len() < *b }) }", nil}, // a closure that borrows, only called
		// A closure held by a variable is refused as one written out is.
		{"func f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "g := " + closure + "; store(mut k, g) }",
			[]string{"closure g captures r by borrow and cannot be passed to store, which keeps f"}},
		// Each way a body keeps a parameter.
		{"func hold(f func() string) func() string { return f }\nfunc f() { " + borrowing + "print(hold(" + closure + ")()) }",
			[]string{"cannot be passed to hold, which keeps f: returned at"}},
		{"func hold(f func() string) { g := f; print(g()) }\nfunc f() { " + borrowing + "hold(" + closure + ") }",
			[]string{"cannot be passed to hold, which keeps f: given to g at"}},
		{"func hold(f func() string) { var g = f; print(g()) }\nfunc f() { " + borrowing + "hold(" + closure + ") }",
			[]string{"cannot be passed to hold, which keeps f: given to g at"}},
		{"func hold(f func() string, fs mut []func() string) { fs.Push(f) }\nfunc f() { fs := []func() string{}; " + borrowing + "hold(" + closure + ", mut fs) }",
			[]string{"cannot be passed to hold, which keeps f: stored at"}},
		{"func hold(f func() string) Keeper { return Keeper{f: f} }\nfunc f() { " + borrowing + "print(hold(" + closure + ").f()) }",
			[]string{"cannot be passed to hold, which keeps f: stored at"}},
		{"func hold(f func() string, ch chan<- func() string) { ch <- f }\nfunc f(ch chan<- func() string) { " + borrowing + "hold(" + closure + ", ch) }",
			[]string{"cannot be passed to hold, which keeps f: sent at"}},
		// A parameter passed on is kept when the one it is passed to is, wherever that function is declared.
		{"func relay(k mut Keeper, f func() string) { store(mut k, f) }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "relay(mut k, " + closure + ") }",
			[]string{"cannot be passed to relay, which keeps f: passed to store, which keeps it at"}},
		{"func f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "relay(mut k, " + closure + ") }\nfunc relay(k mut Keeper, f func() string) { later(mut k, f) }\nfunc later(k mut Keeper, f func() string) { k.f = f }",
			[]string{"cannot be passed to relay, which keeps f: passed to later, which keeps it at"}},
		{"func relay(f func() string) string { return call(f) }\nfunc f() { " + borrowing + "print(relay(" + closure + ")) }", nil},
		// A parameter of a type parameter's type is kept as a function's is.
		{"func put<T>(b mut Box<T>, x T) { b.v = Some(x) }\nfunc f() { b := Box<func() string>{v: None}; " + borrowing + "put(mut b, " + closure + ") }",
			[]string{"cannot be passed to put, which keeps x: stored at"}},
		{"func use<T>(x T) T { return x }\nfunc f() { " + borrowing + "print(use(" + closure + ")()) }",
			[]string{"cannot be passed to use, which keeps x: returned at"}},
		// A call through a value cannot know what its callee keeps: a function that keeps one is not a value, a closure keeps none.
		{"func f() { g := store; k := Keeper{f: func() string { return \"\".Clone() }}; g(mut k, func() string { return \"a\".Clone() }) }",
			[]string{"cannot use store as a value: it keeps its parameter f, assigned to k.f at"}},
		{"func f() { g := call; print(g(func() string { return \"a\".Clone() })) }", nil},
		{"func f() { k := Keeper{f: func() string { return \"\".Clone() }}; set := func(f func() string) { k.f = f }; set(func() string { return \"a\".Clone() }) }",
			[]string{"a closure cannot keep its parameter f: assigned to k.f at"}},
		// A type's method may keep no more than its trait's declaration.
		{"trait Hooks { func (h mut Self) On(f func() string) }\nstruct Hook impl Hooks { f func() string }\nfunc (h mut Hook) On(f func() string) { h.f = f }",
			[]string{"method On keeps its parameter f, assigned to h.f at", "which the trait's On does not"}},
		{"trait Hooks { func (h mut Self) On(f func() string) }\nstruct Hook impl Hooks { n int }\nfunc (h mut Hook) On(f func() string) { h.n = f().Len() }", nil},
		// A closure inside the function keeps the parameter for it: stored there, passed on there, or deferred.
		{"func hold(k mut Keeper, f func() string) { g := func() { k.f = f }; g() }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "hold(mut k, " + closure + ") }",
			[]string{"cannot be passed to hold, which keeps f: assigned to k.f at"}},
		{"func hold(k mut Keeper, f func() string) { g := func() { store(mut k, f) }; g() }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "hold(mut k, " + closure + ") }",
			[]string{"cannot be passed to hold, which keeps f: passed to store, which keeps it at"}},
		{"func hold(k mut Keeper, f func() string) { defer func() { k.f = f }() }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "hold(mut k, " + closure + ") }",
			[]string{"cannot be passed to hold, which keeps f: assigned to k.f at"}},
		{"func hold(f func() string) string { g := func() string { return f() }; return g() }\nfunc f() { " + borrowing + "print(hold(" + closure + ")) }", nil}, // called inside a closure, not kept
		// A parameter yielded by a branch of an if or match expression is kept as a bare one is; a closure yielded by one is refused as a bare one is.
		{"func pick(c bool, f func() string, g func() string) func() string { return if c { f } else { g } }\nfunc f() { " + borrowing + "print(pick(true, " + closure + ", func() string { return \"\".Clone() })()) }",
			[]string{"cannot be passed to pick, which keeps f: returned at"}},
		{"func pick(c bool, f func() string, g func() string) func() string { return match c { true => f, false => g } }\nfunc f() { " + borrowing + "print(pick(true, func() string { return \"\".Clone() }, " + closure + ")()) }",
			[]string{"cannot be passed to pick, which keeps g: returned at"}},
		// A variant holds what it is given, in a generic enum too, and a closure that borrows cannot go in.
		{"enum Slot<T> { Full(T); Empty }\nfunc wrap<T>(x T) Slot<T> { return Full(x) }\nfunc f() { " + borrowing + "sl := wrap(" + closure + "); match sl { Full(g) => print(g()), Empty => {} } }",
			[]string{"cannot be passed to wrap, which keeps x: stored at"}},
		{"enum Slot<T> { Full(T); Empty }\nfunc f() { " + borrowing + "var sl Slot<func() string> = Full(" + closure + "); match sl { Full(g) => print(g()), Empty => {} } }",
			[]string{"closure captures r by borrow and cannot be stored"}},
		{"func f() { " + borrowing + "o := Some(" + closure + "); print(o.IsSome()) }", []string{"closure captures r by borrow and cannot be stored"}},
		// A method that keeps a parameter, on a struct and on a generic type, and a method as a value.
		{"func (k mut Keeper) Set(f func() string) { k.f = f }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "k.Set(" + closure + ") }",
			[]string{"cannot be passed to Set, which keeps f: assigned to k.f at"}},
		{"func (b mut Box<T>) Put(x T) { b.v = Some(x) }\nfunc f() { b := Box<func() string>{v: None}; " + borrowing + "b.Put(" + closure + ") }",
			[]string{"cannot be passed to Put, which keeps x: stored at"}},
		{"func (k mut Keeper) Set(f func() string) { k.f = f }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; set := k.Set; set(func() string { return \"\".Clone() }) }",
			[]string{"cannot use Set as a value: it keeps its parameter f, assigned to k.f at"}},
		// Spawning, a map element and a slice element keep; a parameter given to a variable inside the callee keeps.
		{"func run(f func()) { spawn f() }\nfunc f() { " + borrowing + "run(func() { print(r) }) }", []string{"cannot be passed to run, which keeps f: spawned at"}},
		{"func put(m mut map[string]func() string, f func() string) { m.Insert(\"k\".Clone(), f) }\nfunc f() { m := map[string]func() string{}; " + borrowing + "put(mut m, " + closure + ") }",
			[]string{"cannot be passed to put, which keeps f: stored at"}},
		{"func hold(k mut Keeper, f func() string) { g := f; store(mut k, g) }\nfunc f() { k := Keeper{f: func() string { return \"\".Clone() }}; " + borrowing + "hold(mut k, " + closure + ") }",
			[]string{"cannot be passed to hold, which keeps f: given to g at"}},
		// Passing on through a generic function reaches the one that keeps.
		{"func relay<T>(b mut Box<T>, x T) { put(mut b, x) }\nfunc put<T>(b mut Box<T>, x T) { b.v = Some(x) }\nfunc f() { b := Box<func() string>{v: None}; " + borrowing + "relay(mut b, " + closure + ") }",
			[]string{"cannot be passed to relay, which keeps x: passed to put, which keeps it at"}},
		// A trait's method keeps what its default body keeps, and a type that inherits it is called by it.
		{"trait Hooks { func (h mut Self) On(f func() string) { _ = stash(f) } }\nfunc stash(f func() string) func() string { return f }\nstruct Hook impl Hooks { n int }\nfunc f() { h := Hook{n: 0}; " + borrowing + "h.On(" + closure + ") }",
			[]string{"cannot be passed to On, which keeps f: passed to stash, which keeps it at"}},
		// A deferred call of the parameter runs before the function returns: not kept.
		{"func later(f func()) { defer f() }\nfunc f() { " + borrowing + "later(func() { print(r) }) }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+helpers+tt.src)
		if len(got) != 1 && len(tt.want) > 0 || len(tt.want) == 0 && len(got) != 0 {
			t.Errorf("%s:\n  got %d errors %q\n  want one with %q", tt.src, len(got), got, tt.want)
			continue
		}

		for _, w := range tt.want {
			if !strings.Contains(got[0], w) {
				t.Errorf("%s:\n  got  %q\n  want %q in it", tt.src, got[0], w)
			}
		}
	}
}

// TestKeepsAcrossPackages checks that a function of another package
// that keeps its parameter is known here, with the place it keeps it
// named by its file: rc.New stores what it is given.
func TestKeepsAcrossPackages(t *testing.T) {
	got := checkErrors(t, "package p\nimport \"rc\"\nfunc f() { s := \"a\".Clone(); r := ref s; h := rc.New(func() string { return (*r).Clone() }); print(h.Get()) }\n")
	want := "closure captures r by borrow and cannot be passed to New, which keeps v: stored at rc.snello:"
	if len(got) != 1 || !strings.Contains(got[0], want) {
		t.Errorf("got %q, want one error with %q", got, want)
	}
}

// TestMayHoldClosure checks which parameters can be given a closure:
// those of a function type and of a type parameter's type.
func TestMayHoldClosure(t *testing.T) {
	tp := &TypeParam{}
	for _, tt := range []struct {
		typ  Type
		want bool
	}{
		{NewSignature(nil, nil, nil), true},
		{tp, true},
		{Typ[String], false},
		{NewRef(NewSignature(nil, nil, nil), false), false},
		{NewSlice(NewSignature(nil, nil, nil)), false},
	} {
		if got := mayHoldClosure(tt.typ); got != tt.want {
			t.Errorf("mayHoldClosure(%v) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}
