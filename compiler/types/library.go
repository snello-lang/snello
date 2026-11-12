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

		if t.info&IsInteger != 0 {
			if sig := integerMethod(recv, t, name); sig != nil {
				return sig
			}
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
	case "Extend":
		return changes([]*Var{param("ys", ref(t))}, Unit)
	case "Index":
		return reads([]*Var{param("v", ref(elem))}, NewOption(Typ[Int]))
	case "First", "Last":
		return reads(nil, NewOption(ref(elem)))
	case "Truncate":
		return changes([]*Var{param("n", Typ[Int])}, Unit)
	}

	if b, ok := elem.(*Basic); ok && b.kind == Uint8 { // a []byte itself, not a slice of a type declared over one, whose Eq may be its own
		return byteSliceMethod(t, name)
	}

	return nil
}

// byteSliceMethod: what a []byte has beside every slice's methods, the
// comparison and the search a string has for text.
func byteSliceMethod(t *Slice, name string) *Signature {
	switch name {
	case "Equal":
		return reads([]*Var{param("o", ref(t))}, Typ[Bool])
	case "Find":
		return reads([]*Var{param("sub", ref(t))}, NewOption(Typ[Int]))
	}

	return nil
}

// elementBound names the trait a slice method needs of the element,
// which the checker asks as it asks a generic function's bound: the
// search needs Eq, the sort Ord, and Extend and Clone the Clone they
// copy with.
func elementBound(name string) *Trait {
	switch name {
	case "Contains", "RemoveValue", "Index":
		return EqTrait
	case "Sort":
		return OrdTrait
	case "Extend", "Clone":
		return CloneTrait
	}

	return nil
}

// elementHas reports whether a slice's element t meets the bound tr a
// method asks of it: as a generic function's bound is met; for the
// searches' Eq, also by a slice whose own elements meet it, compared
// element by element, so that a slice of slices can be searched, as
// == on two slices cannot say; for both Eq and Clone, by a borrow of
// what meets it, the pieces Split and Fields lend compared by what they
// view and copied as the borrows they are; and for Clone by any Copy
// element, which copies as its bytes.
func elementHas(t Type, tr *Trait) bool {
	if r, ok := t.(*Ref); ok {
		switch tr {
		case CloneTrait:
			return true // a borrow copies
		case EqTrait:
			return elementHas(r.elem, tr)
		}

		return false // a borrow has no order the backends sort by
	}

	if inner, ok := t.Underlying().(*Slice); ok && tr == EqTrait {
		return elementHas(inner.elem, tr)
	}

	return implements(t, tr) || tr == CloneTrait && IsCopy(t)
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
	case "GetOrInsert":
		return changes([]*Var{keeps("k", t.key), keeps("v", t.value)}, NewRef(t.value, true))
	case "Entry":
		return reads([]*Var{param("i", Typ[Int])}, NewOption(NewTuple(Typ[Int], ref(t.key), ref(t.value))))
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
	case "Trim", "TrimSpace":
		return reads(nil, ref(s))
	case "HasPrefix", "HasSuffix", "Contains":
		return reads([]*Var{param("t", ref(s))}, Typ[Bool])
	case "Index", "LastIndex":
		return reads([]*Var{param("t", ref(s))}, NewOption(Typ[Int]))
	case "Count":
		return reads([]*Var{param("t", ref(s))}, Typ[Int])
	case "TrimPrefix", "TrimSuffix":
		return reads([]*Var{param("p", ref(s))}, ref(s))
	case "Replace":
		return reads([]*Var{param("old", ref(s)), param("new", ref(s)), param("n", Typ[Int])}, s)
	case "ReplaceAll":
		return reads([]*Var{param("old", ref(s)), param("new", ref(s))}, s)
	case "SplitN":
		return reads([]*Var{param("sep", ref(s)), param("n", Typ[Int])}, NewSlice(ref(s)))
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
	case "AndThen":
		return chaining(t, elem, NewOption(mapU))
	case "OrElse":
		return takes(t, []*Var{param("f", NewSignature(nil, nil, t))}, t)
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
	case "Ok":
		return takes(t, nil, NewOption(val))
	case "Err":
		return takes(t, nil, NewOption(t.Underlying().(*Result).err))
	case "AndThen":
		return chaining(t, val, NewResult(mapU, t.Underlying().(*Result).err))
	case "UnwrapOrElse":
		f := NewSignature(nil, []*Var{param("e", t.Underlying().(*Result).err)}, val)
		return takes(t, []*Var{param("f", f)}, val)
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

// chaining returns the signature of AndThen on a receiver of type t:
// it takes the receiver and a function from the value, of type from, to
// out, an option or a result over mapU, and gives back out.
func chaining(t, from, out Type) *Signature {
	f := NewSignature(nil, []*Var{param("v", from)}, out)
	sig := takes(t, []*Var{param("f", f)}, out)
	sig.tparams = []*TypeParam{mapU}
	return sig
}

// integerMethod: the arithmetic every integer type has beside its
// operators, on recv, the integer type or a type declared over it:
// the checked operations give None where the operator would wrap, Abs
// is the signed types' alone, and Clamp holds the value between two.
func integerMethod(recv Type, t *Basic, name string) *Signature {
	switch name {
	case "CheckedAdd", "CheckedSub", "CheckedMul":
		return reads([]*Var{param("o", recv)}, NewOption(recv))
	case "Abs":
		if t.info&IsUnsigned == 0 {
			return reads(nil, recv)
		}
	case "Clamp":
		return reads([]*Var{param("lo", recv), param("hi", recv)}, recv)
	}

	return nil
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
