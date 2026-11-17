// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/types"
)

// TestNullable holds an option to its layout: the pointer itself, null
// for None, for an owned pointer and for a borrow that is the address
// of its place, mut or ref, of a number, a struct or a borrow; a flag
// and the value for anything else, a ref of a string or a slice, which
// is the view with no address, among them.
func TestNullable(t *testing.T) {
	str := types.Typ[types.String]
	num := types.Typ[types.Int]
	tests := []struct {
		name string
		elem types.Type
		want bool
	}{
		{"*int", types.NewPointer(num), true},
		{"mut int", types.NewRef(num, true), true},
		{"ref int", types.NewRef(num, false), true},
		{"ref ref int", types.NewRef(types.NewRef(num, false), false), true},
		{"mut string", types.NewRef(str, true), true},
		{"mut []int", types.NewRef(types.NewSlice(num), true), true},
		{"ref string", types.NewRef(str, false), false},
		{"ref []int", types.NewRef(types.NewSlice(num), false), false},
		{"int", num, false},
		{"string", str, false},
	}
	for _, tt := range tests {
		if got := nullable(types.NewOption(tt.elem)); got != tt.want {
			t.Errorf("nullable(%s?) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestOptionalBorrowIR holds the IR of a function returning an option
// of a ref borrow to the pointer alone, and of one returning an option
// of a string's view to the pair.
func TestOptionalBorrowIR(t *testing.T) {
	ir := emitSource(t, `package main
func first(xs ref []int) ref int? { if len(xs) == 0 { return None }; return Some(ref xs[0]) }
func name(xs ref []string) ref string? { if len(xs) == 0 { return None }; return Some(ref xs[0]) }
func main() { xs := []int{1}; ns := []string{"a"}; print(first(ref xs).IsSome(), name(ref ns).IsSome()) }
`)
	for _, want := range []string{"define internal ptr @sn__first(", "define internal { i1, %sn_string } @sn__name("} {
		if !strings.Contains(ir, want) {
			t.Errorf("the module has no %q", want)
		}
	}
}
