// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
)

// TestHasZero checks which types have a zero value, one type at a
// time, through the one place that asks for it alone: a declaration
// with no value. A type without one is refused with its name.
func TestHasZero(t *testing.T) {
	const decls = `
struct Plain { n int; s string }
struct Owner impl Drop { n int }
func (o mut Owner) Drop() {}
struct Holds { o Owner }
struct Points { p *int }
enum FirstPlain { A(n int); B(p *int) }
enum FirstPointer { C(p *int); D(n int) }
trait Shape { func (s ref Self) Area() f64 }
struct Cell<T> impl Drop { v T }
func (c mut Cell<T>) Drop() {}
enum Handle impl Drop { Live(fd int); Shut }
func (h mut Handle) Drop() {}
struct Def { n int = 1; s string }
struct HoldsDef { d Def }
`
	tests := []struct {
		typ  string
		zero bool
	}{
		{"int", true}, {"bool", true}, {"string", true}, {"f64", true},
		{"*int?", true}, {"Owner?", true}, // an option is None, whatever it holds
		{"[]*int", true}, {"map[string]Owner", true}, // empty, whatever the element
		{"Plain", true}, {"(int, string)", true}, {"[3]int", true},
		{"FirstPlain", true}, {"Result<int, string>", true},
		{"*int", false}, {"chan int", false}, {"func()", false}, {"Shape", false},
		{"Owner", false},                                                                // implements Drop
		{"Holds", false}, {"Points", false}, {"(int, *int)", false}, {"[2]*int", false}, // a part has none
		{"FirstPointer", false}, {"Result<int, Owner>", false},
		{"Cell<int>", false}, {"Handle", false}, // a generic instance and an enum that implement Drop
		{"Def", false}, {"HoldsDef", false}, {"[1]Def", false}, // a default declared, in the type or a part
		{"Def?", true}, {"[]Def", true}, {"map[int]Def", true},
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+"func f() { var x "+tt.typ+"; print(x) }")
		refused := len(got) == 1 && strings.Contains(got[0], "cannot declare x without a value")
		if len(got) > 1 || refused == tt.zero || (len(got) == 1 && !refused) {
			t.Errorf("var x %s: got %q, want a zero value: %v", tt.typ, got, tt.zero)
		}
	}
}

// TestHasOwnZero checks the zero value a type would have but for the
// defaults its fields declare, in it or in a part: a struct with a
// default has one when its fields have, and no other type gains one.
func TestHasOwnZero(t *testing.T) {
	src := `package p
struct Def { n int = 1; s string }
struct HoldsDef { d Def; m int }
struct PtrDef { n int = 1; p *int }
struct DropDef impl Drop { n int = 1 }
func (d mut DropDef) Drop() {}
`
	f, file, err := parser.ParseFile("test.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := libraryConfig().Check("", file, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name      string
		zero, own bool
	}{
		{"Def", false, true},
		{"HoldsDef", false, true},
		{"PtrDef", false, false}, // a pointer has none, default or not
		{"DropDef", false, false},
	} {
		typ := pkg.Scope().Lookup(tt.name).Type()
		if hasZero(typ) != tt.zero || hasOwnZero(typ) != tt.own {
			t.Errorf("%s: hasZero %v, hasOwnZero %v; want %v, %v", tt.name, hasZero(typ), hasOwnZero(typ), tt.zero, tt.own)
		}
	}
}

// TestDropReads checks which types may run a Drop that reads what the
// value borrows when a value of them is dropped: one that implements
// Drop, or holds a part by value that does.
func TestDropReads(t *testing.T) {
	const src = `package p
struct Plain { n int; s string }
struct Owner impl Drop { n int }
func (o mut Owner) Drop() {}
struct Holds { o Owner; n int }
struct Pointed { o *Owner }
enum Either { A(o Owner); B(n int) }
struct Peek impl Drop { src ref string }
func (p mut Peek) Drop() {}
struct Lexer { src ref string }
`
	f, file, err := parser.ParseFile("t.snello", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := libraryConfig().Check("", file, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		want bool
	}{
		{"Plain", false}, {"Lexer", false}, // nothing in it has a Drop
		{"Owner", true}, {"Peek", true}, // it implements Drop
		{"Holds", true}, {"Either", true}, // a part held by value does
		{"Pointed", false}, // what a pointer holds cannot hold a borrow, so its Drop reads none
	}
	for _, tt := range tests {
		typ := pkg.Scope().Lookup(tt.name).Type()
		if got := dropReads(typ); got != tt.want {
			t.Errorf("dropReads(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}

	for _, typ := range []Type{&Option{elem: pkg.Scope().Lookup("Owner").Type()}, &Tuple{elems: []Type{Typ[Int], pkg.Scope().Lookup("Peek").Type()}}, &Slice{elem: pkg.Scope().Lookup("Peek").Type()}} {
		if !dropReads(typ) {
			t.Errorf("dropReads(%s) = false, want true: a part held by value, or an element of a slice, runs one", typ)
		}
	}
}
