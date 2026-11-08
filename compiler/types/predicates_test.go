// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
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
