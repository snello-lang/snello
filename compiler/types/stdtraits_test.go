// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"testing"

	"github.com/snello-lang/snello/compiler/token"
)

// What a derived Error() can print: anything but a function, a channel
// or a trait value of a trait that is none of Error, fmt.Stringer and
// fmt.Formatter, through composites and declared types, with a type
// that holds itself through a pointer cut short. A type that lists
// Formatter prints whatever it holds, since its Format writes the text.
func TestPrintable(t *testing.T) {
	shape := NewTrait(NewTypeName(token.NoPos, "Shape", nil))
	fn := NewSignature(nil, nil, Unit)
	node := NewNamed(NewTypeName(token.NoPos, "Node", nil), nil, nil)
	node.SetUnderlying(NewStruct([]*Var{NewField(token.NoPos, "next", NewOption(NewPointer(node)))}))
	job := NewNamed(NewTypeName(token.NoPos, "Job", nil), NewStruct([]*Var{NewField(token.NoPos, "run", fn)}), nil)
	formatter := NewTrait(NewTypeName(token.NoPos, "Formatter", nil))
	formatter.obj.pkg = &Package{name: "fmt", path: "fmt"} // the trait is known by its name and package
	pretty := NewTrait(NewTypeName(token.NoPos, "Pretty", nil))
	pretty.refines = []*Trait{formatter}
	task := NewNamed(NewTypeName(token.NoPos, "Task", nil), NewStruct([]*Var{NewField(token.NoPos, "run", fn)}), nil)
	task.impls = []*Trait{formatter}
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
		{formatter, true},
		{pretty, true},
		{task, true},
		{NewSlice(task), true},
	}
	for _, tt := range tests {
		if got := printable(tt.typ, nil); got != tt.want {
			t.Errorf("printable(%s) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}
