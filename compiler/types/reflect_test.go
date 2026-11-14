// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// reflectDecls are the declarations the reflection tests share: a
// trait for a bound, a struct with defaults, an enum with fields and a
// recursive one.
const reflectDecls = `
trait Sh { func (s ref Self) A() int }
struct P { x int; name string = "p" }
enum En { Dot; Circle(r int); Pair(int, int) }
enum V { Null; Arr([]V) }
func consume<T>(v T) {}
func zero<T>() T? { return None }
`

// TestReflection checks what a reflecting function may write: a match on
// a type parameter, a field's type and a shape's name, with the arms
// reading the parameter as what they matched; a loop over a struct's or
// a variant's fields, its part's name, type and default, and the field
// it reaches; a value and a variant built from their fields; the arm of
// a match written once per variant.
func TestReflection(t *testing.T) {
	tests := []string{
		"func f<T>(v ref T) int { return match T { int => v, i8 | i16 => int(v), string => len(v), E? => 1, []E => len(v), map[K]V => len(v), *E => 2, P => v.x, _ => 0 } }",
		"func f<T>(v ref T) int { return match T { []E => match E { int => 1, _ => 2 }, _ => 0 } }",
		"func f<T>() T? { return match T { int => Some(1), _ => None } }", // the expected type read as the arm reads it
		"func f<T>(v ref T) int { n := 0; match T { struct => { for f := range fields(T) { n += len(f.name); g(ref v.[f]) } }, _ => {} }; return n }\nfunc g<U>(u ref U) {}",
		"func f<T>() T? { return match T { struct => Some(T{ for f := range fields(T) { [f]: zero<f.type>() ?? return None } }), _ => None } }",
		"func f<T>() T? { return match T { struct => Some(T{ for f := range fields(T) { [f]: match f.type { E? => None, _ => f.default ?? return None } } }), _ => None } }",
		"func f<T>(v ref T) int { return match T { enum => match v { for c := range variants(T) => { n := len(c.name); for f := range fields(c) { g(ref v.[f]) }; n } }, _ => 0 } }\nfunc g<U>(u ref U) {}",
		"func f<T>(name ref string) T? { match T { enum => { for c := range variants(T) { if name == c.name { return Some(c(for f := range fields(c) { zero<f.type>() ?? return None })) } } }, _ => {} }; return None }",
		"func f<T>(v ref T) { match T { []E => g(v), _ => {} } }\nfunc g<U>(u ref []U) {}",
	}
	for _, src := range tests {
		if got := checkErrors(t, "package p\n"+reflectDecls+src); len(got) != 0 {
			t.Errorf("%s:\n  got %q, want no errors", src, got)
		}
	}
}

// TestReflectionErrors checks the mistakes a reflecting function can
// make, each with its message.
func TestReflectionErrors(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"func f<T: Sh>(v ref T) { match T { int => {}, _ => {} } }", "T is never int: int does not implement Sh"},
		{"func f<T>() { match T { []E => match E { E? => {}, _ => {} }, _ => {} } }", "no type is E?: it holds E, the type the match is on"},
		{"func f<T>(v ref T) { T := 1; match T { int => {}, _ => {} } }", "T is a value here, which hides the type parameter"},
		{"func f<T>(v ref T) { match T { int => match T { int => {}, _ => {} }, _ => {} } }", "T is known here to be int: a match on it would take one arm"},
		{"func f<T>(g int) { match g.type { int => {}, _ => {} } }", "g.type is no field's type"},
		{"func f<T>() { x := fields(T) }", "fields gives a type's parts to a loop"},
		{"func f<T>() { for f := range fields(T) {} }", "fields takes a type parameter that an arm struct of a match on it matched"},
		{"func f<T>() { match T { enum => { for f := range fields(T) {} }, _ => {} } }", "fields takes a type parameter that an arm struct of a match on it matched"},
		{"func f<T>() { match T { struct => { for i, f := range fields(T) {} }, _ => {} } }", "a loop over a type's parts names one part"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { print(f) } }, _ => {} } }", "f is a field of a type, known to the compiler alone"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { x := f.size } }, _ => {} } }", "f is a field of a type, which gives f.name, f.type and f.default, not size"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { x := f.type } }, _ => {} } }", "f.type is a type, written where a type is"},
		{"func f<T>(n int) { match T { struct => { for f := range fields(T) { g(ref n.[f]) } }, _ => {} } }\nfunc g<U>(u ref U) {}", "v.[f] reaches a field of a value of the type whose fields f is one of, T, not of int"},
		{"func f<T>(v T) { match T { struct => { for f := range fields(T) { consume(v.[f]) } }, _ => {} } }", "cannot move v.[f] out of v: a field a loop over a type's parts reaches is lent"},
		{"func f<T>(v mut T) { match T { struct => { for f := range fields(T) { consume(take(mut v.[f])) } }, _ => {} } }", "cannot take v.[f]: f.type has no zero value to leave behind; replace(mut v.[f], value) leaves value in its place"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { break } }, _ => {} } }", "break cannot leave a loop over a type's parts"},
		{"func f<T>() { match T { struct => { for f := range fields(T) { defer print(1) } }, _ => {} } }", "defer cannot be inside a loop over a type's parts"},
		{"func f<T>() T? { return match T { struct => Some(T{x: 1}), _ => None } }", "a value of T is built from its fields, T{ for f := range fields(T) { [f]: value } }"},
		{"func f<T>() T? { return match T { struct => Some(T{ for f := range fields(T) { zero<f.type>() ?? return None } }), _ => None } }", "a value of T is built from its fields"},
		{"func f<T>(v ref T) { match T { enum => match v { for c := range variants(T) => {}, _ => {} }, _ => {} } }", "a match written once for each variant has that arm alone"},
		{"func f<T>() T? { match T { enum => { for c := range variants(T) { return Some(c(1)) } }, _ => {} }; return None }", "the variant c is built from its fields"},
		{"func f<T>(v ref T, w ref T) { match T { enum => match v { for c := range variants(T) => { for f := range fields(c) { g(ref w.[f]) } } }, _ => {} } }\nfunc g<U>(u ref U) {}", "v.[f] reaches a field of the variant c in the arm of a match written for it, of the value the match is on"},
		// The arm reads the variant v holds, so nothing changes v while it runs.
		{"func f<T>(v ref T, w ref T) { match T { enum => match v { for c := range variants(T) => { v = w } }, _ => {} } }", "cannot assign to v: the arm written for each variant holds it"},
		{"func f<T>(v mut T, w T) { match T { enum => match v { for c := range variants(T) => { *v = w } }, _ => {} } }", "v is borrowed by the arm written for each variant"},
		{"func f<T>(v T) { match T { enum => match v { for c := range variants(T) => { change(mut v) } }, _ => {} } }\nfunc change<U>(u mut U) {}", "cannot borrow v as mut: v is borrowed as ref by the arm written for each variant"},
		{"func f<T>(v T) { match T { enum => match v { for c := range variants(T) => { consume(v) } }, _ => {} } }", "is borrowed by the arm written for each variant"},
	}
	for _, tt := range tests {
		got := checkErrors(t, "package p\n"+reflectDecls+tt.src)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), tt.want) {
			t.Errorf("%s:\n  got %q\n  want an error containing %q", tt.src, got, tt.want)
		}
	}
}

// TestChooseArm checks which arm a type takes: its own and no other, a
// shape's with its names bound, a kind's, the first that fits.
func TestChooseArm(t *testing.T) {
	e := NewTypeParam(NewTypeName(0, "E", nil), 0)
	celsius := NewNamed(NewTypeName(0, "Celsius", nil), Typ[Float64], nil)
	arms := []TypeArm{{Type: Typ[Int]}, {Type: Typ[Float64]}, {Type: NewSlice(e), Binds: []*TypeParam{e}}, {Kind: "struct"}, {}}
	for _, tt := range []struct {
		t     Type
		arm   int
		bound Type
	}{
		{Typ[Int], 0, nil},
		{celsius, 4, nil}, // a type declared over f64 takes no f64 arm
		{NewSlice(Typ[String]), 2, Typ[String]},
		{NewNamed(NewTypeName(0, "S", nil), NewStruct(nil), nil), 3, nil},
		{Typ[Bool], 4, nil},
	} {
		i, binds := ChooseArm(arms, tt.t)
		if i != tt.arm || tt.bound != nil && !Identical(binds[e], tt.bound) {
			t.Errorf("%s: arm %d with E = %v, want arm %d with %v", tt.t, i, binds[e], tt.arm, tt.bound)
		}
	}

	if i, _ := ChooseArm(arms[:3], Typ[Bool]); i != -1 {
		t.Errorf("bool took arm %d of arms with no wildcard, want none", i)
	}
}
