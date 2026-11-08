// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/snello-lang/snello/compiler/token"

// The methods section 1 of _docs/design/05-standard-library.md
// predeclares on slices, maps, strings, options and results, each a
// mapping onto a runtime function of the same meaning. The library's
// packages are written in Snello under std/ and loaded through the
// Config's Importer.

// BuiltinMethod is builtinMethod for the test that keeps the library's
// documentation of the predeclared methods, std/builtin, in step with
// this table.
func BuiltinMethod(recv Type, name string) *Signature { return builtinMethod(recv, name) }

// builtinMethod returns the signature of a predeclared method on a
// slice, map, channel, string, option or result, or nil. The receiver is the
// value the method is called on, its borrows already stripped.
func builtinMethod(recv Type, name string) *Signature {
	switch t := recv.Underlying().(type) {
	case *Slice:
		return sliceMethod(t, name)
	case *Map:
		return mapMethod(t, name)
	case *Chan:
		return chanMethod(t, name)
	case *Basic:
		if sig := standardMethod(recv, t, name); sig != nil {
			return sig
		}

		if t.kind == String {
			return stringMethod(name)
		}

		if t.kind == Uint8 {
			return byteMethod(name)
		}
	case *Option:
		return optionMethod(recv, t.elem, name)
	case *Result:
		return resultMethod(recv, t.val, name)
	}

	return nil
}

// standardMethod returns the method of a standard trait a number, a
// string or a bool has, with the receiver's type for Self, which may be
// a type declared over the basic one: Eq and Cmp take the other operand
// as a declared type would, by ref for a string and by value for the
// Copy types; Hash gives a u64; Clone gives the type itself. Nil for any
// other name, or a trait the type lacks.
func standardMethod(recv Type, t *Basic, name string) *Signature {
	number, text := t.info&IsNumeric != 0, t.kind == String
	if !number && !text && t.kind != Bool {
		return nil
	}

	other := recv
	if text {
		other = ref(recv)
	}

	switch name {
	case "Eq":
		return reads([]*Var{param("o", other)}, Typ[Bool])
	case "Cmp":
		if !number && !text {
			return nil
		}

		return reads([]*Var{param("o", other)}, Ordering)
	case "Hash":
		return reads(nil, Typ[Uint64])
	case "Clone":
		return reads(nil, recv)
	}

	return nil
}

// Each predeclared method says what it does to its receiver, as a
// declared method's receiver does: reads lends it, changes borrows it
// mut, and takes consumes it, as Expect does the option it opens.
func sliceMethod(t *Slice, name string) *Signature {
	elem := t.elem
	switch name {
	case "Push":
		return changes([]*Var{keeps("v", elem)}, Unit)
	case "Pop":
		return changes(nil, NewOption(elem))
	case "Insert":
		return changes([]*Var{param("i", Typ[Int]), keeps("v", elem)}, Unit)
	case "Remove":
		return changes([]*Var{param("i", Typ[Int])}, elem)
	case "RemoveValue":
		return changes([]*Var{param("v", ref(elem))}, Typ[Bool])
	case "Contains":
		return reads([]*Var{param("v", ref(elem))}, Typ[Bool])
	case "Swap":
		return changes([]*Var{param("i", Typ[Int]), param("j", Typ[Int])}, Unit)
	case "Clear", "Reverse", "Sort":
		return changes(nil, Unit)
	case "SortBy":
		less := NewSignature(nil, []*Var{param("a", ref(elem)), param("b", ref(elem))}, Typ[Bool])
		return changes([]*Var{param("less", less)}, Unit)
	case "Clone":
		return reads(nil, t)
	}

	return nil
}

func mapMethod(t *Map, name string) *Signature {
	switch name {
	case "Get":
		return reads([]*Var{param("k", ref(t.key))}, NewOption(ref(t.value)))
	case "GetMut":
		return changes([]*Var{param("k", ref(t.key))}, NewOption(NewRef(t.value, true)))
	case "Insert":
		return changes([]*Var{keeps("k", t.key), keeps("v", t.value)}, Unit)
	case "Remove":
		return changes([]*Var{param("k", ref(t.key))}, NewOption(t.value))
	case "Contains":
		return reads([]*Var{param("k", ref(t.key))}, Typ[Bool])
	case "Clone":
		return reads(nil, t)
	}

	return nil
}

// chanMethod: Clone gives another handle on the same channel, of the
// same direction.
func chanMethod(t *Chan, name string) *Signature {
	if name == "Clone" {
		return reads(nil, t)
	}

	return nil
}

// stringMethod: Fields and Split yield borrows into the string. Until
// the iterator trait exists they are typed as a slice of
// borrows, a type a program cannot write, which ranges as the iterator
// will.
func stringMethod(name string) *Signature {
	s := Typ[String]
	switch name {
	case "Len":
		return reads(nil, Typ[Int])
	case "ToUpper", "ToLower":
		return reads(nil, s)
	case "Fields", "Lines":
		return reads(nil, NewSlice(ref(s)))
	case "Runes":
		return reads(nil, NewSlice(aliases[1]))
	case "Split":
		return reads([]*Var{param("sep", ref(s))}, NewSlice(ref(s)))
	case "SplitOnce":
		return reads([]*Var{param("sep", ref(s))}, NewOption(NewTuple(ref(s), ref(s))))
	case "Trim":
		return reads(nil, ref(s))
	case "HasPrefix", "HasSuffix", "Contains":
		return reads([]*Var{param("t", ref(s))}, Typ[Bool])
	}

	return nil
}

func optionMethod(t, elem Type, name string) *Signature {
	switch name {
	case "Expect":
		return takes(t, []*Var{param("msg", ref(Typ[String]))}, elem)
	case "Unwrap":
		return takes(t, nil, elem)
	case "UnwrapOr":
		return takes(t, []*Var{param("v", elem)}, elem)
	case "IsSome", "IsNone":
		return reads(nil, Typ[Bool])
	case "Map":
		return mapping(t, elem, mapU, NewOption(mapU))
	}

	return nil
}

func resultMethod(t, val Type, name string) *Signature {
	switch name {
	case "Expect":
		return takes(t, []*Var{param("msg", ref(Typ[String]))}, val)
	case "Unwrap":
		return takes(t, nil, val)
	case "UnwrapOr":
		return takes(t, []*Var{param("v", val)}, val)
	case "IsOk", "IsErr":
		return reads(nil, Typ[Bool])
	case "Map":
		return mapping(t, val, mapU, NewResult(mapU, t.Underlying().(*Result).err))
	case "MapErr":
		return mapping(t, t.Underlying().(*Result).err, mapU, NewResult(val, mapU))
	}

	return nil
}

// mapU is the type parameter of Map and MapErr, the type their function
// gives back, which each call infers from its argument. One parameter
// serves every signature: an inference is keyed by it, and nothing is
// called from inside the methods, so no call cycle runs through it.
var mapU = NewTypeParam(NewTypeName(token.NoPos, "U", nil), 0)

// mapping returns the signature of Map or MapErr on a receiver of type
// t: it takes the receiver and a function from the part of type from to
// a value of the type parameter u, and gives back result, which names
// u. The function's own parameter is taken by value, as the method
// takes its receiver.
func mapping(t, from Type, u *TypeParam, result Type) *Signature {
	f := NewSignature(nil, []*Var{param("v", from)}, u)
	sig := takes(t, []*Var{param("f", f)}, result)
	sig.tparams = []*TypeParam{u}
	return sig
}

// byteMethod: the character classes a parser asks of a byte, ASCII
// only, as a byte is one.
func byteMethod(name string) *Signature {
	switch name {
	case "IsDigit", "IsSpace", "IsLetter", "IsUpper", "IsLower":
		return reads(nil, Typ[Bool])
	}

	return nil
}

// The receivers of the methods that lend are shared: the checker reads
// only whether the borrow is mut, and the emitter lowers these methods
// by name. A method that takes its receiver names the type, which the
// move pass needs.
var (
	refReceiver = param("", NewRef(Typ[Any], false))
	mutReceiver = param("", NewRef(Typ[Any], true))
)

func reads(params []*Var, results Type) *Signature {
	return NewSignature(refReceiver, params, results)
}

func changes(params []*Var, results Type) *Signature {
	return NewSignature(mutReceiver, params, results)
}

func takes(recv Type, params []*Var, results Type) *Signature {
	return NewSignature(param("", recv), params, results)
}

func param(name string, t Type) *Var {
	return NewVar(token.NoPos, name, t)
}

// keeps is a parameter whose argument the method stores, which a
// closure capturing a local cannot be.
func keeps(name string, t Type) *Var {
	v := NewVar(token.NoPos, name, t)
	v.kept = true
	return v
}

func ref(t Type) *Ref {
	return NewRef(t, false)
}
