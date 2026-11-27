// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// passingPrelude declares an iterator trait with default adapters, an
// iterator that borrows a slice, a struct that borrows a string, and
// generic functions of every shape the rules tell apart.
const passingPrelude = `
trait It<T> {
	func (it mut Self) Next() T?
	func (it Self) Map<U>(f func(T) U) Mapped<Self, T, U> { return Mapped{inner: it, f: f} }
	func (it mut Self) Collect() []T {
		out := []T{}
		for { out.Push(it.Next() ?? break) }
		return out
	}
}
struct Mapped<I: It<T>, T, U> impl It<U> { inner I; f func(T) U }
func (m mut Mapped<I, T, U>) Next() U? { return m.inner.Next().Map(m.f) }
struct Walk impl It<ref int> { xs ref []int; i int }
func (w mut Walk) Next() ref int? {
	if w.i >= len(w.xs) { return None }
	w.i += 1
	return Some(ref w.xs[w.i - 1])
}
struct View { s ref string }
trait Show { func (s ref Self) Text() string }
struct Shown impl Show { s ref string }
func (v ref Shown) Text() string { return (*v.s).Clone() }
struct Wrap<T: Show> impl Show { x T }
func (w ref Wrap<T>) Text() string { return w.x.Text() }
struct Cell<T> { v T? }
struct Sink<T> { c mut Cell<T> }
struct Keyed<T> { m map[int]T }
func Id<T>(v T) T { return v }
func Into<T>(dst mut T, v ref T) {}
func Back<T>(v ref T) T? { return None }
func Put<T>(s Sink<T>, x T) { s.c.v = Some(x) }
func Keys<T>(v T) Keyed<T> { return Keyed<T>{m: map[int]T{}} }
func Hide<T: Show + Copy>(x ref T) Show { return Wrap<T>{x: *x} }
func HideOn<T: Show + Copy>(x ref T) Show { return Hide(x) }
func Later<T: Copy>(x ref T) func() T { y := *x; return func() T { return y } }
func Twice<T>(xs ref []T) []T { return []T{} }
func consume(s string) {}
`

// iterPrelude builds a deque the cases iterate over with the library's
// iter adapters.
const iterPrelude = `package p
import ("deque"; "fmt"; "iter")
func even(x ref ref int) bool { return **x % 2 == 0 }
func twice(x ref int) int { return *x * 2 }
func ints() deque.Deque<int> { d := deque.New<int>(); d.PushBack(1); return d }
struct Holder { it iter.Map<deque.Iter<int>, ref int, int> }
`

// TestPassing checks how a borrow a type argument holds is followed
// through generic code from outside: an instance holds what its
// arguments hold and keeps none of it in a map, a channel or a pointer;
// a generic call's result holds what its arguments hold, and an
// argument lent mut takes what the others hold.
func TestPassing(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// An instance holds what its arguments hold, and keeps none of it where the checker cannot follow it.
		{"func f() { xs := []int{1, 2}; m := Walk{xs: ref xs, i: 0}.Map(func(x ref int) int { return *x }); print(m.Collect()) }", nil},
		{"func f() { xs := []int{1}; m := Walk{xs: ref xs, i: 0}.Map(func(x ref int) int { return *x }); xs.Push(2); print(m.Collect()) }", []string{"cannot borrow xs as mut: xs is borrowed as ref by m"}},
		{"func f() { ys := []int{1}; m := Walk{xs: ref ys, i: 0}.Map(func(x ref int) int { return *x }); { xs := []int{1}; m = Walk{xs: ref xs, i: 0}.Map(func(x ref int) int { return *x }) }; print(m.Collect()) }", []string{"cannot keep a borrow of xs in m: xs is dropped at the end of its block"}},
		{"func f(s ref string) { k := Keys(View{s: s}); print(k) }", []string{"a map cannot hold View: it holds a borrow"}},
		{"func f(s ref string) { var k Keyed<View>; print(k) }", []string{"a map cannot hold View: it holds a borrow"}},
		{"func f() { xs := []int{1}; rs := Walk{xs: ref xs, i: 0}.Collect(); xs = []int{2}; print(rs) }", []string{"cannot assign to xs: xs is borrowed by rs"}}, // rs holds what the temporary held, not the temporary
		{"func f() { xs := []int{1}; w := Walk{xs: ref xs, i: 0}; rs := w.Collect(); w = Walk{xs: ref xs, i: 0}; print(rs, w.i) }", nil},
		{"struct Outer2 { k Late<View> }\nstruct Late<T> { m map[int]T }", []string{"a map cannot hold View: it holds a borrow"}}, // an instance named before its declaration

		// A generic call passes what its arguments hold to its result and to an argument lent mut.
		{"func f(s ref string) { v := Id(View{s: s}); print(v) }", nil},
		{"func f() View { t := \"a\".Clone(); return Id(View{s: ref t}) }", []string{"cannot return a borrow of local t"}},
		{"func f() View? { t := \"a\".Clone(); return Back(View{s: ref t}) }", []string{"cannot return a borrow of local t"}},
		{"struct Slot<T> { v T }\nfunc set<T>(c mut Slot<T>, x T) { c.v = x }\nfunc mk() string { return \"m\".Clone() }\nfunc f(p ref string) int { c := Slot<ref string>{v: p}; set(mut c, mk()); return len(c.v) }", []string{"cannot keep a borrow of a temporary: mk() is dropped when the statement ends"}}, // a value made here, which the store would keep
		{"func f() { s := \"a\".Clone(); o := View{s: ref s}; { t := \"b\".Clone(); Into(mut o, View{s: ref t}) }; print(o.s.Len()) }", []string{"cannot keep a borrow of t in o: t is dropped at the end of its block"}},
		{"func f(o mut View, s ref string) { Into(o, View{s: s}) }", []string{"cannot keep a borrow in o: Into may store what View{...} holds in it, where the checker cannot follow it"}},
		{"func f() { c := Cell<ref string>{v: None}; t := \"t\".Clone(); r := ref t; Put<ref string>(Sink<ref string>{c: mut c}, r); print(c.v) }", []string{"cannot pass Sink<ref string>{...} to Put beside r: Sink<ref string>{...} holds a mut borrow, through which Put may store what r holds"}},
		{"func f() { s := \"a\".Clone(); r := Id<ref string>(ref s.Clone()); print(*r) }", []string{"cannot keep a borrow of a temporary: s.Clone() is dropped when the statement ends"}}, // ref of a temporary is the value a bare T takes
		{"struct Peek impl Drop { s ref string }\nfunc (p mut Peek) Drop() { print(p.s.Len()) }\nfunc Many<T>(x T) []T { return []T{x} }\nfunc f() { t := \"a\".Clone(); xs := Many(Peek{s: ref t}); consume(t) }", []string{"cannot move t: t is borrowed by xs"}},
	}
	for _, tt := range tests {
		expectErrors(t, prelude+passingPrelude+tt.src, tt.src, tt.want)
	}
}

// TestBindings checks that matching a declared signature against the
// same signature with arguments in place finds what each type parameter
// stands for, through every kind of type, once each.
func TestBindings(t *testing.T) {
	p := NewTypeParam(NewTypeName(0, "T", nil), 0)
	q := NewTypeParam(NewTypeName(0, "U", nil), 1)
	view := &Ref{elem: Typ[String]}
	decl := &Signature{
		params:  []*Var{NewVar(0, "a", &Slice{elem: p}), NewVar(0, "b", &Map{key: Typ[Int], value: &Tuple{elems: []Type{p, q}}})},
		results: &Option{elem: q},
	}
	inst := &Signature{
		params:  []*Var{NewVar(0, "a", &Slice{elem: view}), NewVar(0, "b", &Map{key: Typ[Int], value: &Tuple{elems: []Type{view, Typ[Int]}}})},
		results: &Option{elem: Typ[Int]},
	}

	bs := bindings(decl, inst)
	if len(bs) != 2 || bs[0].p != p || bs[0].arg != view || bs[1].p != q || bs[1].arg != Typ[Int] {
		t.Errorf("bindings = %v, want T = ref string, U = int", bs)
	}
}

// TestMayGive checks that a type gives a value of a type parameter when
// it mentions it, or through a bound that does, and not otherwise.
func TestMayGive(t *testing.T) {
	tr := NewTrait(NewTypeName(0, "It", nil))
	elem := NewTypeParam(NewTypeName(0, "T", nil), 0)
	tr.tparams = []*TypeParam{elem}
	other := NewTypeParam(NewTypeName(0, "W", nil), 1)
	other.setBounds([]*Trait{NewTrait(NewTypeName(0, "Writer", nil))})

	tests := []struct {
		name string
		t    Type
		want bool
	}{
		{"T itself", elem, true},
		{"[]T", &Slice{elem: elem}, true},
		{"Self of It<T>", tr.self, true},
		{"a parameter bounded by a trait without T", other, false},
		{"int", Typ[Int], false},
	}
	for _, tt := range tests {
		if got := mayGive(tt.t, elem, nil); got != tt.want {
			t.Errorf("mayGive(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestIterRefusals checks that every way an iterator over a borrow, or
// an adapter over one, could outlive or race its source is refused:
// the forms issue #168 lists, for each adapter where it asks.
func TestIterRefusals(t *testing.T) {
	adapters := []string{
		"d.Iter().Map(twice)", "d.Iter().Filter(even)", "d.Iter().Take(1)", "d.Iter().Skip(0)",
		"d.Iter().Enumerate()", "d.Iter().Zip(d.Iter())", "d.Iter().Chain(d.Iter())",
	}

	type refusal struct{ src, want string }
	var tests []refusal
	for _, a := range adapters {
		tests = append(tests,
			refusal{"func f() { d := ints(); it := " + a + "; d.PushBack(2); fmt.Println(it.Count()) }", "cannot borrow d as mut: d is borrowed as ref by it"},
			refusal{"func f() { d := ints(); it := " + a + "; e := d; fmt.Println(it.Count(), e.Len()) }", "cannot move d: d is borrowed by it"},
		)
	}

	tests = append(tests,
		refusal{"func f() { e := ints(); it := e.Iter().Map(twice); { d := ints(); it = d.Iter().Map(twice) }; fmt.Println(it.Count()) }", "cannot keep a borrow of d in it: d is dropped at the end of its block"},
		refusal{"func f() iter.Filter<deque.Iter<int>, ref int> { d := ints(); return d.Iter().Filter(even) }", "cannot return a borrow of local d"},
		refusal{"func f() { d := ints(); hs := []Holder{}; hs.Push(Holder{it: d.Iter().Map(twice)}) }", "a slice cannot hold Holder: it holds a borrow"}, // a struct holding an adapter holds its borrow
		refusal{"func f() { d := ints(); its := []iter.Take<deque.Iter<int>, ref int>{}; its.Push(d.Iter().Take(1)) }", "a slice cannot hold iter.Take<deque.Iter<int>, ref int>: it holds a borrow"},
		refusal{"func f() { m := map[int]iter.Skip<deque.Iter<int>, ref int>{}; fmt.Println(m) }", "a map cannot hold iter.Skip<deque.Iter<int>, ref int>: it holds a borrow"},
		refusal{"func f() { c := make(chan iter.Skip<deque.Iter<int>, ref int>); fmt.Println(c) }", "a channel cannot carry iter.Skip<deque.Iter<int>, ref int>: it holds a borrow"},
		refusal{"func f() { d := ints(); it := d.Iter().Skip(0); spawn func() { fmt.Println(it.Count()) }() }", "cannot spawn with it"},
		refusal{"func f() { d := ints(); var s fmt.Stringer = d.Iter().Skip(0); fmt.Println(s) }", "cannot use"},
		refusal{"func f() { var xs []ref int; { d := ints(); xs = d.Iter().Collect() }; fmt.Println(xs) }", "a slice cannot hold a borrow"},
		refusal{"func f() { d := ints(); xs := d.Iter().Collect(); d.PushBack(3); fmt.Println(xs) }", "cannot borrow d as mut: d is borrowed as ref by xs"},
		refusal{"func f() { d := ints(); n := 0; fmt.Println(d.Iter().Map(func(x ref int) int { n += *x; return n }).Count()) }", "closure captures n by borrow and cannot be passed to Map, which keeps f"},
		refusal{"func f() { d := ints(); fmt.Println(d.Iter().Filter(func(x ref ref int) bool { d.PushBack(1); return true }).Count()) }", "cannot borrow d as mut: d is borrowed as ref"}, // the iterator borrows d for the call, which the closure would change; the closure is refused as a kept borrow too
		refusal{"func f() { d := ints(); it := d.Iter().Filter(even); if v := d.GetMut(0) { *v = 2 }; fmt.Println(it.Count()) }", "cannot borrow d as mut: d is borrowed as ref by it"},
		refusal{"struct Keyed<T> { m map[int]T }\nfunc keep<T>(v T) Keyed<T> { return Keyed<T>{m: map[int]T{}} }\nfunc f() { d := ints(); fmt.Println(keep(d.Iter())) }", "a map cannot hold deque.Iter<int>: it holds a borrow"},
	)

	for _, tt := range tests {
		got := checkErrors(t, iterPrelude+tt.src)
		if len(got) == 0 || !strings.Contains(got[0], tt.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got, tt.want)
		}
	}
}

// expectErrors checks src, a program whose cases are shown as name, and
// holds what the checker reports to want, one message each, in order.
func expectErrors(t *testing.T, src, name string, want []string) {
	t.Helper()
	got := checkErrors(t, src)
	if len(got) != len(want) {
		t.Errorf("%s:\n  got %d errors %q\n  want %d %q", name, len(got), got, len(want), want)
		return
	}

	for i := range got {
		if !strings.Contains(got[i], want[i]) {
			t.Errorf("%s:\n  got  %q\n  want %q", name, got[i], want[i])
		}
	}
}
