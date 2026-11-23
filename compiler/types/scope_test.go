// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"slices"
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// TestVisible lists a scope's names and its enclosing scopes', the
// innermost first and each scope's by name, a name the inner scope
// declares hiding the outer one's; and Names a scope's own alone.
func TestVisible(t *testing.T) {
	outer := NewScope(nil)
	inner := NewScope(outer)
	for _, obj := range []Object{NewVar(token.NoPos, "b", Typ[Int]), NewVar(token.NoPos, "x", Typ[Int]), NewVar(token.NoPos, "a", Typ[Int])} {
		outer.Insert(obj)
	}

	hiding := NewVar(token.NoPos, "x", Typ[String])
	inner.Insert(hiding)
	inner.Insert(NewVar(token.NoPos, "y", Typ[Int]))
	var names []string
	for _, obj := range inner.Visible() {
		names = append(names, obj.Name())
		if obj.Name() == "x" && obj != hiding {
			t.Error("the outer x is visible beside the inner one")
		}
	}

	if want := []string{"x", "y", "a", "b"}; !slices.Equal(names, want) {
		t.Errorf("Visible: %q, want %q", names, want)
	}

	if got := outer.Names(); !slices.Equal(got, []string{"a", "b", "x"}) {
		t.Errorf("Names: %q, want the scope's own, sorted", got)
	}
}
