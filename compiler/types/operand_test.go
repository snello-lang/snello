// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// isRune and isRuneSlice name the operands that convert to a string as
// runes: a rune, which is an i32, a type declared over it, and a slice
// of either.
func TestRuneOperands(t *testing.T) {
	letter := NewNamed(NewTypeName(token.NoPos, "Letter", nil), Typ[Int32], nil)
	tests := []struct {
		typ         Type
		rune, slice bool
	}{
		{Typ[Int32], true, false},
		{aliases[1], true, false},
		{letter, true, false},
		{Typ[Int], false, false},
		{Typ[Uint8], false, false},
		{NewSlice(Typ[Int32]), false, true},
		{NewSlice(letter), false, true},
		{NewSlice(Typ[Uint8]), false, false},
		{Typ[String], false, false},
	}
	for _, tt := range tests {
		if got := isRune(tt.typ); got != tt.rune {
			t.Errorf("isRune(%s) = %v, want %v", tt.typ, got, tt.rune)
		}

		if got := isRuneSlice(tt.typ); got != tt.slice {
			t.Errorf("isRuneSlice(%s) = %v, want %v", tt.typ, got, tt.slice)
		}
	}
}
