// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"sort"
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// genericPrelude declares the generic types the cases use: a list with
// methods, a recursive enum, a pair, and a type parameter bounded by
// Copy.
const genericPrelude = `
struct List<T> { items []T }
func (l mut List<T>) Push(v T) { l.items.Push(v) }
func (l ref List<T>) Len() int { return len(l.items) }
func (l ref List<T>) Get(i int) ref T? {
	if i < len(l.items) {
		return Some(ref l.items[i])
	}

	return None
}
enum Tree<T> { Leaf(v T); Branch(left *Tree<T>, right *Tree<T>) }
struct Pair<A, B> { a A; b B }
struct Copied<T: Copy> { v T }
func takeInts(l List<int>) {}
`

// TestGenericTypes checks generic declarations and their instances:
// that an instance has the declaration's fields, variants and methods
// with the arguments in place, that two uses of the same arguments are
// the same type, that a type parameter is an owned type that moves
// unless bounded by Copy, and the errors of a wrong use.
func TestGenericTypes(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// Instances: fields, methods and variants with the arguments in place.
		{"func f() { l := List<int>{}; l.Push(1); print(l.Len()) }", nil},
		{"func f() { l := List<string>{}; l.Push(\"a\"); if s := l.Get(0) { print(s.Len()) } }", nil},
		{"func f() { p := Pair<int, string>{a: 1, b: \"x\"}; print(p.b.Len()) }", nil},
		{"func f() { l := List<int>{}; l.Push(\"a\") }", []string{"cannot use a value of type string as int in argument"}},
		{"func f() { var t Tree<string> = Leaf(\"a\"); match t { Leaf(v) => print(v.Len()), Branch(_, _) => {} } }", nil},
		{"func f() { var t Tree<int> = Branch(&Leaf(1), &Leaf(2)); match t { Leaf(_) => {} } }", []string{"match is not exhaustive: missing Branch(_, _)"}},
		{"func f() { t := Leaf(1); match t { Leaf(v) => print(v + 1), Branch(_, _) => {} } }", nil}, // inferred from the value

		// The same arguments are the same type; others are not.
		{"func f() { takeInts(List<int>{}) }", nil},
		{"func f() { takeInts(List<string>{}) }", []string{"List<string>"}},
		{"func f() { var l List<int> = List<string>{}; print(l.Len()) }", []string{"List<string>"}},

		// An instance is Copy when its fields are, and moves otherwise.
		{"func f() { p := Pair<int, bool>{a: 1, b: true}; q := p; print(p.a); print(q.b) }", nil},
		{"func f() { l := List<string>{}; m := l; print(l.Len()); print(m.Len()) }", []string{"use of moved value l"}},

		// Inside the declaration, a type parameter moves unless bounded by Copy.
		{"func (l mut List<T>) Twice(v T) { l.Push(v); l.Push(v) }", []string{"use of moved value v"}},
		{"func (c Copied<T>) Two() (T, T) { return (c.v, c.v) }", nil},
		{"func (l ref List<E>) First() E? { return None }", nil}, // a method may name the parameter differently

		// Wrong uses.
		{"func f() { l := List<int, int>{}; print(l) }", []string{"List takes 1 type argument: List<T>"}},
		{"func f() { var l List; print(l) }", []string{"List takes 1 type argument: List<T>"}},
		{"func f() { var n Node<int>; print(n) }", []string{"Node is not generic: write Node"}},
		{"func (l ref List<int>) Sum() int { return 0 }", []string{"a method's receiver names the type's parameters: List<T>"}},
		{"func (l ref List<T, U>) Both() {}", []string{"List takes 1 type argument: List<T>"}}, // one error, not one per name
		{"struct D<T, T> { a T }", []string{"duplicate type parameter T"}},
		{"func f(s ref string) { l := List<ref string>{}; print(l.Len()) }", []string{"a type argument cannot hold a borrow"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+genericPrelude+tt.src)
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

// TestTypeCycles checks the declarations that describe no type a
// program can hold: a generic type whose arguments grow each time it
// refers to itself, directly or through another, and a type that
// contains itself by value, generic or not. A recursive type through
// a pointer, and a package type sharing a parameter's name, are fine.
func TestTypeCycles(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"struct S<T> { s *S<S<T>>? }", []string{"S<T> refers to itself as S<S<T>>, whose arguments grow with each instance"}},
		{"struct S<T> { v T; s (S<S<T>>)? }", []string{"S<T> refers to itself as S<S<T>>"}},
		{"struct A<T> { b *B<T>? }\nstruct B<T> { a *A<B<T>>? }", []string{"B<T> refers to itself as A<B<T>>"}},
		{"struct R { v int; r R? }", []string{"invalid recursive type R: it contains itself; hold it through a pointer, *R"}},
		{"struct S<T> { v T; s (S<T>)? }", []string{"invalid recursive type S: it contains itself"}},
		{"struct Box<T> { v T }\nstruct W { b Box<W> }", []string{"invalid recursive type W: it contains itself"}},
		// A call that grows its own type parameter, directly or through another function or a method.
		{"struct W<T> { v T }\nfunc Deep<T>(x T, n int) int { if n == 0 { return 0 }; return Deep(W{v: x}, n - 1) }", []string{"the call Deep<W<T>> grows the type parameter T each time around: it would need instances without end"}},
		{"struct W<T> { v T }\nfunc A<T>(x T) { B(W{v: x}) }\nfunc B<T>(x T) { A(x) }", []string{"the call B<W<T>> grows the type parameter T each time around"}},
		{"struct L<T> { v T }\nfunc (l L<T>) X() { Y(L<L<T>>{v: l}) }\nfunc Y<E>(l L<E>) { l.X() }", []string{"the call Y<L<T>> grows the type parameter T each time around"}},
		{"func Same<T>(x T, n int) int { if n == 0 { return 0 }; return Same(x, n - 1) }", nil},
		{"struct W<T> { v T }\nfunc Wrap<T>(x T) W<T> { return W{v: x} }\nfunc f() { print(Wrap(Wrap(1))) }", nil}, // the growth is in the caller, finite
		{"enum E { One(e E); Two }", []string{"invalid recursive type E: it contains itself"}},
		{"struct N<T> { v T; next *N<T>? }\nstruct Tr<K, V> { root *N<(K, V)>? }\nstruct Ok { t Tr<int, string> }", nil},
		{"struct R { v int; r *R?; rs []R; m map[int]R }", nil},
		{"struct T { n int }\nstruct L<T> { items []T }\nfunc (l ref L<T>) Len() int { return len(l.items) }", nil},
	}
	for _, tt := range tests {
		got := checkErrors(t, "package p\n"+tt.src+"\n")
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

// genericFuncs declares the generic functions the cases call, among
// them example 08's Map and Filter.
const genericFuncs = `
enum Opt<T> { Nothing; Just(v T) }
func Id<T>(x T) T { return x }
func Two<T>(a T, b T) {}
func Map<T, U>(l List<T>, f func(T) U) List<U> {
	out := List<U>{}
	for _, v := range l.items { out.Push(f(v)) }
	return out
}
func Filter<T>(l List<T>, keep func(ref T) bool) List<T> {
	out := List<T>{}
	for _, v := range l.items {
		if keep(ref v) { out.Push(v) }
	}
	return out
}
func Empty<T>() List<T> { return List<T>{} }
func First<T>(xs ref []T) ref T? {
	if len(xs) == 0 { return None }
	return Some(ref xs[0])
}
func Fill<T>(xs mut []T, v T) { xs.Push(v) }
func Put<T>(v T, xs mut []T) { xs.Push(v) }
struct View { s ref string }
`

// TestGenericFunctions checks generic functions and inference: type
// arguments from the values, from the context, written at the call, or
// some of each; the moves and borrows of a generic call, which are a
// plain call's; and the variants and struct literals whose type
// arguments are inferred the same way.
func TestGenericFunctions(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// From the values.
		{"func f() { n := Id(1); print(n + 1) }", nil},
		{"func f() { l := List<int>{}; l.Push(1); d := Map(l, func(x int) string { return \"s\" }); print(d.Len()) }", nil},
		{"func f() { l := List<int>{}; takeInts(Map(l, func(x int) string { return \"s\" })) }", []string{"List<string>"}},
		{"func f() { l := List<int>{}; e := Filter(l, func(x ref int) bool { return x > 0 }); takeInts(e) }", nil},
		{"func f() { xs := []f64{}; Fill(mut xs, 1) }", nil},
		{"func f() { xs := []f64{}; Put(1, mut xs) }", nil},     // the literal waits for xs, though it comes first, and is an f64
		{"func f(r ref int) { n := Id(r); print(n + 1) }", nil}, // T is what r borrows; a borrow of a Copy value reads as the value
		{"func f() { l := List<int>{}; m := Map(l, func(x int) ref string { return \"s\" }); print(m.Len()) }", []string{"cannot use a value of type func(int) ref string as func(int) string in argument"}}, // U is never a borrow
		{"func f(s ref string) { v := Id(View{s: s}); print(v) }", []string{"a type argument cannot hold View: it holds a borrow"}},
		{"func Use<T>(v ref T) int { return 1 }\nfunc f(s ref string) int { return Use(View{s: s}) }", nil},                                                                         // borrowed and kept nowhere: safe
		{"func Keep2<T>(a ref T, v T) {}\nfunc f(s ref string) { Keep2(View{s: s}, View{s: s}) }", []string{"a type argument cannot hold View: it holds a borrow"}},                 // taken by value
		{"func Use2<T>(v ref T) int { return 1 }\nfunc f(s ref string) int { return Use2<View>(View{s: s}) }", nil},                                                                 // written, the same rule
		{"func Into<T>(dst mut T, v ref T) {}\nfunc f(s ref string) { o := View{s: s}; Into(mut o, View{s: s}) }", []string{"a type argument cannot hold View: it holds a borrow"}}, // through dst, v could be kept
		{"func Swap<T>(a mut T, b mut T) {}\nfunc f(s ref string) { o := View{s: s}; p := View{s: s}; Swap(mut o, mut p) }", []string{"a type argument cannot hold View: it holds a borrow"}},
		{"func Touch<T>(a mut T) {}\nfunc f(s ref string) { o := View{s: s}; Touch(mut o) }", nil},                                                                                       // alone, mut keeps nothing new
		{"func Back<T>(v ref T) T? { return None }\nfunc f(s ref string) { v := Back(View{s: s}); print(v.IsNone()) }", []string{"a type argument cannot hold View: it holds a borrow"}}, // the result could carry it out
		{"func f() { xs := []f64{}; Fill(mut xs, \"a\") }", []string{"cannot use a value of type string as f64 in argument"}},

		// From the context, or written at the call.
		{"func f() { var e List<int> = Empty(); print(e.Len()) }", nil},
		{"func f() { e := Empty<int>(); takeInts(e) }", nil},
		{"func f() { l := List<int>{}; m := Map<int, bool>(l, func(x int) bool { return true }); print(m.Len()) }", nil},
		{"func f() { l := List<int>{}; m := Map<int>(l, func(x int) bool { return true }); takeInts(m) }", []string{"List<bool>"}}, // a prefix is written, the rest inferred
		{"func f() { n := (Id)(1); print(n + 1) }", nil},
		{"func Size<E>(l ref List<E>) int { return l.Len() }\nfunc (l ref List<T>) Count() int { return Size(l) }", nil}, // E is T, the declaration's own parameter
		{"func f() { e := Empty(); print(e) }", []string{"cannot infer the type parameter T of Empty: give it, Empty<...>"}},
		{"func f() { n := Id<int, int>(1); print(n) }", []string{"Id takes 1 type argument"}},
		{"func f() { takeInts<int>(List<int>{}) }", []string{"takeInts is not generic and takes no type arguments"}},
		{"func f() { g := Id; print(g) }", []string{"cannot use the generic function Id without calling it"}},

		// Moves and borrows are a plain call's.
		{"func f() { l := List<int>{}; d := Map(l, func(x int) int { return x }); print(l.Len()); print(d.Len()) }", []string{"use of moved value l"}},
		{"func f() { s := \"a\".Clone(); Two(s, s) }", []string{"use of moved value s"}},
		{"func f() { xs := []int{1}; if v := First(xs) { print(v) } }", nil},
		{"func f() { xs := []int{1}; r := First(xs); xs.Push(2); print(r) }", []string{"cannot borrow xs as mut: xs is borrowed as ref by r"}},

		// Inside a generic function, as inside a generic type.
		{"func Dup<T>(x T) (T, T) { return (x, x) }", []string{"use of moved value x"}},
		{"func Dup<T: Copy>(x T) (T, T) { return (x, x) }", nil},
		// A method may have type parameters of its own, on a generic type or a plain one.
		{"func (l ref List<T>) Conv<U>(f func(ref T) U) List<U> { out := List<U>{}; for _, v := range ref l.items { out.Push(f(v)) }; return out }\nfunc g() { l := List<int>{}; s := l.Conv(func(x ref int) string { return \"s\" }); print(s.Len()) }", nil},
		{"func (l ref List<T>) Conv<U>(f func(ref T) U) List<U> { out := List<U>{}; return out }\nfunc g() { l := List<int>{}; takeInts(l.Conv(func(x ref int) string { return \"s\" })) }", []string{"List<string>"}},
		{"func (n ref Node) With<U>(u U) (int, U) { return (n.n, u) }\nfunc g() { n := Node{value: \"a\", next: None, n: 1}; i, s := n.With(\"x\"); print(i); print(s.Len()) }", nil},
		{"func (n ref Node) With<U>(u U) U { return u }\nfunc g() { n := Node{value: \"a\", next: None, n: 1}; print(n.With<int, int>(1)) }", []string{"n.With takes 1 type argument"}},
		{"func (n ref Node) With<U>(u U) U { return u }\nfunc g() { n := Node{value: \"a\", next: None, n: 1}; w := n.With; print(w) }", []string{"cannot use the generic method With without calling it"}},

		// Variants and struct literals infer the same way.
		{"func f() { var t Tree<f64> = Leaf(1); print(t) }", nil},
		{"func f() { o := Nothing; print(o) }", []string{"cannot infer the type parameter T of Nothing: the context gives it no type"}},
		{"func f() { var o Opt<int> = Nothing; match o { Nothing => {}, Just(v) => print(v + 1) } }", nil},
		{"func f() { p := Pair{a: 1, b: \"x\"}; print(p.b.Len()) }", nil},
		{"func f() { l := List{items: []int{1}}; takeInts(l) }", nil},
		{"func f() { p := Pair{a: 1}; print(p) }", []string{"cannot infer the type parameter B of Pair"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+genericPrelude+genericFuncs+tt.src)
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

// TestInstances checks that each call of a generic function records its
// type arguments and its signature with them in place, which the
// backend writes one copy of the function for.
func TestInstances(t *testing.T) {
	src := "package p\nfunc Id<T>(x T) T { return x }\nfunc f() { a := Id(1); b := Id(\"s\"); print(a); print(b) }\n"
	f, file, err := parser.ParseFile("t.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	info := &Info{}
	if _, err := Check(file, f, info); err != nil {
		t.Fatal(err)
	}

	var got []string
	for id, inst := range info.Instances {
		got = append(got, id.Name+"<"+typeList(inst.TypeArgs)+"> "+inst.Type.String())
	}

	sort.Strings(got)
	want := []string{"Id<int> func(int) int", "Id<string> func(string) string"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("instances:\n  got  %q\n  want %q", got, want)
	}
}
