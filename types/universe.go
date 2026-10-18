// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/token"

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
}

func init() {
	Universe = NewScope(nil)
	for _, t := range Typ[Bool:] { // Invalid is not a name
		def(NewTypeName(token.NoPos, t.name, t))
	}

	for _, t := range aliases {
		def(NewTypeName(token.NoPos, t.name, t))
	}

	def(NewConst(token.NoPos, "true", Typ[Bool], "true"))
	def(NewConst(token.NoPos, "false", Typ[Bool], "false"))
	for _, b := range predeclaredBuiltins {
		def(&Builtin{object{b.name, nil, token.NoPos}, b.id})
	}

	SomeVariant = NewVariant(token.NoPos, "Some", nil, 0, nil)
	NoneVariant = NewVariant(token.NoPos, "None", nil, 1, nil)
	OkVariant = NewVariant(token.NoPos, "Ok", nil, 0, nil)
	ErrVariant = NewVariant(token.NoPos, "Err", nil, 1, nil)
	def(SomeVariant)
	def(NoneVariant)
	def(OkVariant)
	def(ErrVariant)
}

// def declares a predeclared object; a duplicate is a bug in this file.
func def(obj Object) {
	if Universe.Insert(obj) != nil {
		panic("types: " + obj.Name() + " predeclared twice")
	}
}
