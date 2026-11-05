// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"

	"github.com/snello-lang/snello/compiler/parser"
	"github.com/snello-lang/snello/compiler/token"
)

// TestIsSend covers the type kinds of the table in
// _docs/design/03-ownership-across-tasks.md, over the shared fixtures of
// types_test.go, a declared type with a local field standing for
// rc.Rc, a recursive enum, a trait refining Send and type parameters
// with and without the bound.
func TestIsSend(t *testing.T) {
	// struct Counted { n int; _ local }, a type whose count is not atomic.
	counted := NewNamed(NewTypeName(token.NoPos, "Counted", nil), nil, nil)
	counted.SetUnderlying(NewStruct([]*Var{NewField(token.NoPos, "n", integer), NewField(token.NoPos, "_", Typ[Local])}))

	// struct Holder { c Counted }: not Send through its field.
	holder := NewNamed(NewTypeName(token.NoPos, "Holder", nil), nil, nil)
	holder.SetUnderlying(NewStruct([]*Var{NewField(token.NoPos, "c", counted)}))

	// enum Expr { Lit(f64); Neg(*Expr) } reaches itself through a pointer.
	expr := NewNamed(NewTypeName(token.NoPos, "Expr", nil), nil, nil)
	expr.SetUnderlying(NewEnum([]*Variant{
		NewVariant(token.NoPos, "Lit", expr, 0, []*Var{NewField(token.NoPos, "", f64)}),
		NewVariant(token.NoPos, "Neg", expr, 1, []*Var{NewField(token.NoPos, "", NewPointer(expr))}),
	}))

	// struct Ring { next *Ring?; c Counted }: a cycle, and not Send for another reason.
	ring := NewNamed(NewTypeName(token.NoPos, "Ring", nil), nil, nil)
	ring.SetUnderlying(NewStruct([]*Var{
		NewField(token.NoPos, "next", NewOption(NewPointer(ring))),
		NewField(token.NoPos, "c", counted),
	}))

	// trait Shape {} and trait Job: Send {}
	shape := NewTrait(NewTypeName(token.NoPos, "Shape", nil))
	job := NewTrait(NewTypeName(token.NoPos, "Job", nil))
	job.refines = []*Trait{SendTrait}

	// T, and S: Send
	free := NewTypeParam(NewTypeName(token.NoPos, "T", nil), 0)
	bounded := NewTypeParam(NewTypeName(token.NoPos, "S", nil), 0)
	bounded.setBounds([]*Trait{SendTrait})

	tests := []struct {
		typ  Type
		want bool
		why  string
	}{
		{Typ[Int], true, ""},
		{Typ[Bool], true, ""},
		{Typ[String], true, ""},
		{Typ[Error], true, ""},
		{Typ[Local], false, "it stays in its task"},
		{nodeID, true, ""},
		{point, true, ""},
		{node, true, ""}, // a string and an owned pointer
		{tok, true, ""},
		{state, true, ""},
		{expr, true, ""},
		{counted, false, "Counted stays in its task"},
		{holder, false, "Counted stays in its task"}, // the field's type, not the holder
		{ring, false, "Counted stays in its task"},
		{NewPointer(node), true, ""},
		{NewPointer(counted), false, "Counted stays in its task"},
		{NewRef(node, false), false, "ref Node is a borrow"},
		{NewRef(node, true), false, "mut Node is a borrow"},
		{NewOption(NewRef(integer, false)), false, "ref int is a borrow"},
		{NewSlice(str), true, ""},
		{NewSlice(counted), false, "Counted stays in its task"},
		{NewArray(integer, 3), true, ""},
		{NewMap(str, counted), false, "Counted stays in its task"},
		{NewMap(counted, str), false, "Counted stays in its task"},
		{NewChan(SendRecv, counted), true, ""}, // the handle crosses; the element type is refused where the channel is made
		{NewTuple(integer, str), true, ""},
		{NewTuple(integer, NewRef(str, false)), false, "ref string is a borrow"},
		{NewResult(integer, Typ[Error]), true, ""},
		{NewResult(counted, Typ[Error]), false, "Counted stays in its task"},
		{Unit, true, ""},
		{NewSignature(nil, nil, nil), true, ""},
		{shape, false, "Shape does not refine Send"},
		{job, true, ""},
		{free, false, "T is not bounded by Send"},
		{bounded, true, ""},
	}
	for _, tt := range tests {
		if got := IsSend(tt.typ); got != tt.want {
			t.Errorf("IsSend(%s) = %v, want %v", tt.typ, got, tt.want)
		}

		if got := NotSend(tt.typ); got != tt.why {
			t.Errorf("NotSend(%s) = %q, want %q", tt.typ, got, tt.why)
		}
	}
}

// TestSendInPrograms checks what a program may write: Send as a bound
// and a refinement, never in an impl list; local only in the library;
// and rc's handles not Send, named by their package.
func TestSendInPrograms(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"trait Job: Send { func (j ref Self) Run() }", nil},
		{"func Go<T: Send>(v T) {}", nil},
		{"struct Task impl Send { n int }", []string{"Send is not listed: a type is Send when every field is"}},
		{"struct Task { pin local }", []string{"local is a field of the standard library's types that stay in their task; a program does not write it"}},
		{"func f(v local) {}", []string{"local is a field of the standard library's types that stay in their task; a program does not write it"}},
	}
	for _, tt := range tests {
		got := checkErrors(t, "package main\n"+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%q: got %d errors %q, want %q", tt.src, len(got), got, tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%q: error %q does not contain %q", tt.src, got[i], tt.want[i])
			}
		}
	}

	// rc.Rc<int> and rc.Weak<int>, as the program's check sees them.
	f, file, err := parser.ParseFile("test.snello", []byte("package main\nimport \"rc\"\nstruct Box<T> { v T }\nstruct Both { r rc.Rc<int>; w rc.Weak<int>; xs []rc.Rc<string>; b Box<rc.Rc<int>>; ok Box<int> }\nfunc main() {}"))
	if err != nil {
		t.Fatal(err)
	}

	pkg, err := libraryConfig().Check("", file, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	both := pkg.Scope().Lookup("Both").Type().(*Named)
	want := map[string]string{ // the handle the program sees, never rc's cell
		"r":  "rc.Rc<int> stays in its task",
		"w":  "rc.Weak<int> stays in its task",
		"xs": "rc.Rc<string> stays in its task",
		"b":  "rc.Rc<int> stays in its task",
		"ok": "",
	}
	st := both.Underlying().(*Struct)
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if why := NotSend(f.typ); why != want[f.name] {
			t.Errorf("NotSend(%s) = %q, want %q", f.typ, why, want[f.name])
		}

		if IsSend(f.typ) != (want[f.name] == "") {
			t.Errorf("IsSend(%s) = %v", f.typ, IsSend(f.typ))
		}
	}

	if IsSend(both) {
		t.Error("Both is Send")
	}
}
