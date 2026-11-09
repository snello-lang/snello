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
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+decls+"func f() { var x "+tt.typ+"; print(x) }")
		refused := len(got) == 1 && strings.Contains(got[0], "has no zero value")
		if len(got) > 1 || refused == tt.zero || (len(got) == 1 && !refused) {
			t.Errorf("var x %s: got %q, want a zero value: %v", tt.typ, got, tt.zero)
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

	for _, typ := range []Type{&Option{elem: pkg.Scope().Lookup("Owner").Type()}, &Tuple{elems: []Type{Typ[Int], pkg.Scope().Lookup("Peek").Type()}}} {
		if !dropReads(typ) {
			t.Errorf("dropReads(%s) = false, want true: a part held by value runs one", typ)
		}
	}
}
