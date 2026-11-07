// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// What a derived Error() can print: anything but a function, a channel
// or a trait value of a trait that is neither Error nor fmt.Stringer,
// through composites and declared types, with a type that holds itself
// through a pointer cut short.
func TestPrintable(t *testing.T) {
	shape := NewTrait(NewTypeName(token.NoPos, "Shape", nil))
	fn := NewSignature(nil, nil, Unit)
	node := NewNamed(NewTypeName(token.NoPos, "Node", nil), nil, nil)
	node.SetUnderlying(NewStruct([]*Var{NewField(token.NoPos, "next", NewOption(NewPointer(node)))}))
	job := NewNamed(NewTypeName(token.NoPos, "Job", nil), NewStruct([]*Var{NewField(token.NoPos, "run", fn)}), nil)
	tests := []struct {
		typ  Type
		want bool
	}{
		{Typ[Int], true},
		{Typ[String], true},
		{fn, false},
		{NewChan(SendRecv, Typ[Int]), false},
		{ErrorTrait, true},
		{shape, false},
		{NewSlice(fn), false},
		{NewSlice(Typ[Int]), true},
		{NewOption(NewRef(Typ[String], false)), true},
		{NewMap(Typ[String], fn), false},
		{NewResult(Typ[Int], shape), false},
		{NewTuple(Typ[Int], fn), false},
		{NewArray(Typ[Int], 3), true},
		{node, true},
		{job, false},
		{NewPointer(job), false},
	}
	for _, tt := range tests {
		if got := printable(tt.typ, nil); got != tt.want {
			t.Errorf("printable(%s) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}
