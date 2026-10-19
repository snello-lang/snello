// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"fmt"
	"testing"

	"github.com/teak-lang/teak/token"
)

// The declared types the tests share: struct Node { value string; next *Node? },
// struct Point { x, y f64 }, enum Token { Num(f64); Plus }, enum State
// { Closed; Open(Node) }, type NodeID int.
var (
	node    = NewNamed(NewTypeName(token.NoPos, "Node", nil), nil, nil)
	point   = NewNamed(NewTypeName(token.NoPos, "Point", nil), nil, nil)
	tok     = NewNamed(NewTypeName(token.NoPos, "Token", nil), nil, []*Trait{CopyTrait, EqTrait})
	state   = NewNamed(NewTypeName(token.NoPos, "State", nil), nil, nil)
	nodeID  = NewNamed(NewTypeName(token.NoPos, "NodeID", nil), Typ[Int], nil)
	f64     = Typ[Float64]
	str     = Typ[String]
	integer = Typ[Int]
)

func init() {
	node.SetUnderlying(NewStruct([]*Var{
		NewField(token.NoPos, "value", str),
		NewField(token.NoPos, "next", NewOption(NewPointer(node))),
	}))
	point.SetUnderlying(NewStruct([]*Var{NewField(token.NoPos, "x", f64), NewField(token.NoPos, "y", f64)}))
	tok.SetUnderlying(NewEnum([]*Variant{
		NewVariant(token.NoPos, "Num", tok, 0, []*Var{NewField(token.NoPos, "", f64)}),
		NewVariant(token.NoPos, "Plus", tok, 1, nil),
	}))
	state.SetUnderlying(NewEnum([]*Variant{
		NewVariant(token.NoPos, "Closed", state, 0, nil),
		NewVariant(token.NoPos, "Open", state, 1, []*Var{NewField(token.NoPos, "n", node)}),
	}))
}

func TestTypeString(t *testing.T) {
	tests := []struct {
		typ  Type
		want string
	}{
		{Typ[Int], "int"},
		{Typ[Float64], "f64"},
		{aliases[0], "byte"},
		{Typ[Error], "error"},
		{node, "Node"},
		{NewPointer(node), "*Node"},
		{NewOption(NewPointer(node)), "*Node?"},
		{NewOption(NewRef(str, false)), "ref string?"},
		{NewRef(NewOption(NewPointer(node)), true), "mut (*Node?)"}, // a borrow of an optional, not an optional borrow
		{NewPointer(NewOption(integer)), "*(int?)"},
		{NewRef(NewSlice(integer), true), "mut []int"},
		{NewSlice(NewOption(integer)), "[]int?"},
		{NewOption(NewSlice(integer)), "([]int)?"},
		{NewArray(f64, 4), "[4]f64"},
		{NewMap(str, NewOption(integer)), "map[string]int?"},
		{NewChan(SendRecv, NewStruct(nil)), "chan struct{}"},
		{NewChan(SendOnly, integer), "chan<- int"},
		{NewChan(RecvOnly, integer), "<-chan int"},
		{Unit, "()"},
		{NewTuple(NewPointer(node), NewOption(NewPointer(node))), "(*Node, *Node?)"},
		{NewResult(Typ[Int64], Typ[Error]), "Result<i64, error>"},
		{NewResult(Unit, state), "Result<(), State>"},
		{NewSignature(nil, nil, nil), "func()"},
		{NewSignature(nil, nil, &Tuple{}), "func()"}, // an empty tuple built by hand is still Unit
		{NewSignature(nil, []*Var{NewVar(token.NoPos, "n", NewRef(node, false))}, nil), "func(ref Node)"},
		{NewSignature(nil, []*Var{NewVar(token.NoPos, "a", integer), NewVar(token.NoPos, "b", integer)}, Typ[Bool]), "func(int, int) bool"},
		{NewSignature(nil, nil, NewTuple(integer, str)), "func() (int, string)"},
		{NewOption(NewSignature(nil, nil, nil)), "(func())?"},
		{node.Underlying(), "struct{value string; next *Node?}"},
		{tok.Underlying(), "enum{Num(f64); Plus}"},
		{state.Underlying(), "enum{Closed; Open(Node)}"},
	}
	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestIdentical(t *testing.T) {
	tests := []struct {
		x, y Type
		want bool
	}{
		{Typ[Int], Typ[Int], true},
		{Typ[Int], Typ[Int64], false},
		{Typ[Uint8], aliases[0], true}, // byte is u8
		{Typ[Int32], aliases[1], true}, // rune is i32
		{nodeID, Typ[Int], false},      // a named type is only itself
		{node, node, true},
		{node, point, false},
		{NewPointer(node), NewPointer(node), true},
		{NewPointer(node), NewOption(NewPointer(node)), false},
		{NewRef(node, false), NewRef(node, false), true},
		{NewRef(node, false), NewRef(node, true), false},
		{NewSlice(integer), NewSlice(integer), true},
		{NewSlice(integer), NewArray(integer, 1), false},
		{NewArray(integer, 2), NewArray(integer, 3), false},
		{NewMap(str, integer), NewMap(str, integer), true},
		{NewMap(str, integer), NewMap(str, f64), false},
		{NewChan(SendRecv, integer), NewChan(SendOnly, integer), false},
		{NewTuple(integer, str), NewTuple(integer, str), true},
		{NewTuple(integer, str), NewTuple(str, integer), false},
		{Unit, NewTuple(), true},
		{NewResult(integer, Typ[Error]), NewResult(integer, Typ[Error]), true},
		{NewResult(integer, Typ[Error]), NewResult(integer, state), false},
		{NewSignature(nil, []*Var{NewVar(token.NoPos, "a", integer)}, nil), NewSignature(nil, []*Var{NewVar(token.NoPos, "b", integer)}, nil), true}, // names do not matter
		{NewSignature(nil, []*Var{NewVar(token.NoPos, "a", integer)}, nil), NewSignature(nil, []*Var{NewVar(token.NoPos, "a", NewRef(integer, false))}, nil), false},
		{NewSignature(nil, nil, integer), NewSignature(nil, nil, nil), false},
		{NewStruct(nil), NewStruct(nil), true},
		{NewStruct([]*Var{NewField(token.NoPos, "x", integer)}), NewStruct([]*Var{NewField(token.NoPos, "y", integer)}), false},
		{node.Underlying(), node.Underlying(), true},
	}
	for _, tt := range tests {
		if got := Identical(tt.x, tt.y); got != tt.want {
			t.Errorf("Identical(%s, %s) = %v, want %v", tt.x, tt.y, got, tt.want)
		}

		if got := Identical(tt.y, tt.x); got != tt.want {
			t.Errorf("Identical(%s, %s) = %v, want %v (reversed)", tt.y, tt.x, got, tt.want)
		}
	}
}

func TestAssignableTo(t *testing.T) {
	ids := NewNamed(NewTypeName(token.NoPos, "IDs", nil), NewSlice(integer), nil)
	tests := []struct {
		v, t Type
		want bool
	}{
		{integer, integer, true},
		{integer, nodeID, false},       // both named: NodeID(n) is needed
		{NewSlice(integer), ids, true}, // an unnamed literal takes the name
		{ids, NewSlice(integer), true},
		{ids, NewSlice(f64), false},
		{NewRef(node, true), NewRef(node, false), true}, // mut passes as ref
		{NewRef(node, false), NewRef(node, true), false},
		{NewRef(node, true), NewRef(point, false), false},
		{node, NewRef(node, false), false}, // the implicit borrow is a rule of calls
		{NewChan(SendRecv, integer), NewChan(SendOnly, integer), true},
		{NewChan(SendRecv, integer), NewChan(RecvOnly, integer), true},
		{NewChan(SendOnly, integer), NewChan(SendRecv, integer), false},
		{NewChan(SendRecv, integer), NewChan(SendOnly, f64), false},
		{NewChan(SendRecv, integer), NewNamed(NewTypeName(token.NoPos, "Ch", nil), NewChan(SendOnly, integer), nil), true},                                                      // through a name
		{NewNamed(NewTypeName(token.NoPos, "Bi", nil), NewChan(SendRecv, integer), nil), NewChan(RecvOnly, integer), true},                                                      // a named channel narrows
		{NewNamed(NewTypeName(token.NoPos, "Bi", nil), NewChan(SendRecv, integer), nil), NewNamed(NewTypeName(token.NoPos, "Ch", nil), NewChan(SendOnly, integer), nil), false}, // two names never assign
		{NewPointer(node), NewOption(NewPointer(node)), false},                                                                                                                  // Some(p) is written
		{Unit, Unit, true},
	}
	for _, tt := range tests {
		if got := AssignableTo(tt.v, tt.t); got != tt.want {
			t.Errorf("AssignableTo(%s, %s) = %v, want %v", tt.v, tt.t, got, tt.want)
		}
	}
}

func TestIsCopy(t *testing.T) {
	// enum Expr { Lit(f64); Neg(*Expr) } reaches itself through a pointer.
	expr := NewNamed(NewTypeName(token.NoPos, "Expr", nil), nil, nil)
	expr.SetUnderlying(NewEnum([]*Variant{
		NewVariant(token.NoPos, "Lit", expr, 0, []*Var{NewField(token.NoPos, "", f64)}),
		NewVariant(token.NoPos, "Neg", expr, 1, []*Var{NewField(token.NoPos, "", NewPointer(expr))}),
	}))
	tests := []struct {
		typ  Type
		want bool
	}{
		{Typ[Int], true},
		{Typ[Bool], true},
		{Typ[Float64], true},
		{aliases[0], true},
		{Typ[String], false},
		{Typ[Error], false},
		{Typ[Invalid], false},
		{nodeID, true},
		{point, true},  // two f64 fields
		{node, false},  // holds a string and a pointer
		{tok, true},    // every payload is Copy
		{state, false}, // Open holds a Node
		{expr, false},  // Neg holds a pointer
		{NewPointer(point), false},
		{NewRef(node, false), true}, // any number of refs may exist
		{NewRef(node, true), false}, // exactly one mut
		{NewOption(integer), true},
		{NewOption(str), false},
		{NewResult(integer, Typ[Error]), false},
		{NewResult(integer, tok), true},
		{NewSlice(integer), false},
		{NewArray(integer, 3), true},
		{NewArray(str, 3), false},
		{NewMap(str, integer), false},
		{NewChan(SendRecv, str), true},
		{NewTuple(integer, f64), true},
		{NewTuple(integer, str), false},
		{Unit, true},
		{NewSignature(nil, nil, nil), true},
	}
	for _, tt := range tests {
		if got := IsCopy(tt.typ); got != tt.want {
			t.Errorf("IsCopy(%s) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestUniverse(t *testing.T) {
	tests := []struct {
		name string
		kind string // the object's kind, "" for undefined
		typ  string // String of the object's type, "" when it has none
	}{
		{"int", "*types.TypeName", "int"},
		{"i32", "*types.TypeName", "i32"},
		{"byte", "*types.TypeName", "byte"},
		{"rune", "*types.TypeName", "rune"},
		{"f64", "*types.TypeName", "f64"},
		{"bool", "*types.TypeName", "bool"},
		{"string", "*types.TypeName", "string"},
		{"error", "*types.TypeName", "error"},
		{"true", "*types.Const", "bool"},
		{"false", "*types.Const", "bool"},
		{"len", "*types.Builtin", ""},
		{"take", "*types.Builtin", ""},
		{"min", "*types.Builtin", ""},
		{"max", "*types.Builtin", ""},
		{"Some", "*types.Variant", ""},
		{"None", "*types.Variant", ""},
		{"Ok", "*types.Variant", ""},
		{"Err", "*types.Variant", ""},
		{"nil", "", ""},
		{"int32", "", ""},
		{"float64", "", ""},
		{"append", "", ""},
		{"Option", "", ""},
		{"Arena", "", ""},
	}
	for _, tt := range tests {
		obj := Universe.Lookup(tt.name)
		if obj == nil {
			if tt.kind != "" {
				t.Errorf("Universe.Lookup(%q) = nil, want %s", tt.name, tt.kind)
			}

			continue
		}

		if kind := fmt.Sprintf("%T", obj); kind != tt.kind {
			t.Errorf("Universe.Lookup(%q) is %s, want %s", tt.name, kind, tt.kind)
		}

		typ := ""
		if obj.Type() != nil {
			typ = obj.Type().String()
		}

		if typ != tt.typ {
			t.Errorf("Universe.Lookup(%q).Type() = %q, want %q", tt.name, typ, tt.typ)
		}
	}

	if !Identical(Universe.Lookup("byte").Type(), Universe.Lookup("u8").Type()) {
		t.Error("byte and u8 are not identical")
	}

	if n := Universe.Len(); n != 49 {
		t.Errorf("universe has %d names, want 49: 19 types, 2 constants, 8 builtins, 7 variants, 13 traits", n)
	}
}

func TestScope(t *testing.T) {
	pkg := NewScope(Universe)
	fn := NewScope(pkg)
	x := NewVar(1, "x", integer)
	if old := pkg.Insert(x); old != nil {
		t.Fatalf("Insert(x) returned %v, want nil", old)
	}

	if old := pkg.Insert(NewVar(9, "x", str)); old != x {
		t.Errorf("Insert(second x) returned %v, want the first x", old)
	}

	if s, obj := fn.LookupParent("x"); s != pkg || obj != x {
		t.Errorf("LookupParent(x) = %v, %v; want the package scope and x", s, obj)
	}

	if s, obj := fn.LookupParent("int"); s != Universe || obj == nil {
		t.Errorf("LookupParent(int) = %v, %v; want the universe", s, obj)
	}

	if fn.Lookup("x") != nil {
		t.Error("Lookup in the inner scope found the outer x")
	}

	if s, obj := fn.LookupParent("y"); s != nil || obj != nil {
		t.Errorf("LookupParent(y) = %v, %v; want nil, nil", s, obj)
	}

	if fn.Parent() != pkg || Universe.Parent() != nil {
		t.Error("Parent chain is wrong")
	}
}
