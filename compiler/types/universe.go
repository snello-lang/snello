// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/compiler/token"

// Universe is the outermost scope: the names every Teak file sees
// without an import. Its contents are section 1 of
// _docs/design/05-standard-library.md, and nothing else: there is no
// prelude.
var Universe *Scope

// The variants of the built-in enums, so the checker can compare by
// identity: Some, None, Ok and Err.
var (
	SomeVariant *Variant
	NoneVariant *Variant
	OkVariant   *Variant
	ErrVariant  *Variant
)

// Ordering is what Cmp returns, an enum of three cases, predeclared
// with them: the Ord trait's method and the Cmp of every number and
// string return it.
var (
	Ordering       *Named
	LessVariant    *Variant
	EqualVariant   *Variant
	GreaterVariant *Variant
)

// The standard traits, predeclared so that impl lists and bounds can
// name them: the markers and the operator traits of section 1 of
// _docs/design/05-standard-library.md. Each marker asks for one method
// over Self, given in init; Copy asks for none, being a property of a
// type's structure. The predeclared types implement them by structure
// (stdtraits.go), and a declared type by listing them, writing the
// method or deriving it from its fields. The operator traits get their
// methods with the operators. Send asks for no method either: a type
// is Send by its structure (send.go), and listing it is refused.
var (
	CopyTrait  = standardTrait("Copy")
	CloneTrait = standardTrait("Clone")
	EqTrait    = standardTrait("Eq")
	OrdTrait   = standardTrait("Ord")
	HashTrait  = standardTrait("Hash")
	DropTrait  = standardTrait("Drop")
	ErrorTrait = standardTrait("Error")
	AddTrait   = standardTrait("Add")
	SubTrait   = standardTrait("Sub")
	MulTrait   = standardTrait("Mul")
	DivTrait   = standardTrait("Div")
	RemTrait   = standardTrait("Rem")
	NegTrait   = standardTrait("Neg")
	SendTrait  = standardTrait("Send")
)

var standardTraits = [...]*Trait{
	CopyTrait, CloneTrait, EqTrait, OrdTrait, HashTrait, DropTrait, ErrorTrait,
	AddTrait, SubTrait, MulTrait, DivTrait, RemTrait, NegTrait, SendTrait,
}

var predeclaredBuiltins = [...]struct {
	name string
	id   BuiltinID
}{
	{"len", BuiltinLen},
	{"cap", BuiltinCap},
	{"make", BuiltinMake},
	{"take", BuiltinTake},
	{"panic", BuiltinPanic},
	{"print", BuiltinPrint},
	{"min", BuiltinMin},
	{"max", BuiltinMax},
	{"close", BuiltinClose},
}

func init() {
	Universe = NewScope(nil)
	for _, t := range Typ[Bool:Any] { // Invalid and Never are not names
		def(NewTypeName(token.NoPos, t.name, t))
	}

	def(NewTypeName(token.NoPos, "any", Typ[Any]))     // the type of a variadic parameter of the library, nowhere else
	def(NewTypeName(token.NoPos, "local", Typ[Local])) // a field of a library type that stays in its task, nowhere else

	for _, t := range aliases {
		def(NewTypeName(token.NoPos, t.name, t))
	}

	def(NewConst(token.NoPos, "true", Typ[Bool], "true"))
	def(NewConst(token.NoPos, "false", Typ[Bool], "false"))
	for _, b := range predeclaredBuiltins {
		def(NewBuiltin(b.name, b.id))
	}

	for _, t := range standardTraits {
		def(t.obj)
	}

	Ordering = NewNamed(NewTypeName(token.NoPos, "Ordering", nil), nil, nil)
	LessVariant = NewVariant(token.NoPos, "Less", Ordering, 0, nil)
	EqualVariant = NewVariant(token.NoPos, "Equal", Ordering, 1, nil)
	GreaterVariant = NewVariant(token.NoPos, "Greater", Ordering, 2, nil)
	Ordering.SetUnderlying(NewEnum([]*Variant{LessVariant, EqualVariant, GreaterVariant}))
	def(Ordering.obj)
	def(LessVariant)
	def(EqualVariant)
	def(GreaterVariant)

	requires(DropTrait, "Drop", true, nil, Unit)
	requires(CloneTrait, "Clone", false, nil, CloneTrait.self)
	requires(EqTrait, "Eq", false, []*Var{otherParam(EqTrait)}, Typ[Bool])
	requires(OrdTrait, "Cmp", false, []*Var{otherParam(OrdTrait)}, Ordering)
	requires(HashTrait, "Hash", false, nil, Typ[Uint64])
	requires(ErrorTrait, "Error", false, nil, Typ[String])
	refines(OrdTrait, EqTrait)  // what is ordered can be compared
	refines(HashTrait, EqTrait) // equal values must hash alike
	for _, o := range operators {
		operator(o.trait, o.name, true)
	}

	operator(NegTrait, "Neg", false)

	SomeVariant = NewVariant(token.NoPos, "Some", nil, 0, nil)
	NoneVariant = NewVariant(token.NoPos, "None", nil, 1, nil)
	OkVariant = NewVariant(token.NoPos, "Ok", nil, 0, nil)
	ErrVariant = NewVariant(token.NoPos, "Err", nil, 1, nil)
	def(SomeVariant)
	def(NoneVariant)
	def(OkVariant)
	def(ErrVariant)
}

// requires gives a standard trait its one method: name, on a ref or
// mut receiver of Self, with the given parameters and result.
func requires(t *Trait, name string, mut bool, params []*Var, result Type) {
	recv := NewVar(token.NoPos, "s", NewRef(t.self, mut))
	t.required = []*Func{NewFunc(token.NoPos, name, NewSignature(recv, params, result))}
}

// refines makes the standard trait t refine base, with the resolution
// order a declared trait would get from resolveTraitLists.
func refines(t, base *Trait) {
	t.refines = []*Trait{base}
	t.order = linearize(t.refines)
}

// operator gives an operator trait its method: name, taking Self by
// value and, for a binary operator, the other operand, of type Self
// until an argument says otherwise, and returning Self.
func operator(t *Trait, name string, binary bool) {
	var params []*Var
	if binary {
		params = []*Var{NewVar(token.NoPos, "o", t.self)}
	}

	recv := NewVar(token.NoPos, "a", t.self)
	t.required = []*Func{NewFunc(token.NoPos, name, NewSignature(recv, params, t.self))}
}

// otherParam is the parameter of a method comparing a value with
// another of its type: o ref Self.
func otherParam(t *Trait) *Var {
	return NewVar(token.NoPos, "o", NewRef(t.self, false))
}

// def declares a predeclared object; a duplicate is a bug in this file.
func def(obj Object) {
	if Universe.Insert(obj) != nil {
		panic("types: " + obj.Name() + " predeclared twice")
	}
}
