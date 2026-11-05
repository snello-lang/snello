// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The shapes of _docs/design/07-lowering.md. A borrow, an owned
// pointer and a channel handle are a ptr; a string and every slice
// are three words; an option of a pointer or of a mut borrow is the
// pointer itself, null for None, and any other option is a flag and
// the value; a result and an enum are a tag and a payload of words
// large enough for their largest case, read and written through a
// pointer as the case's own struct; a function value is a function
// pointer and a box of words; a trait value is a table pointer and a
// data pointer. A declared struct or enum is a named type of the
// module, which the IR lets refer to itself through a ptr.

// irType writes the IR type of a Snello type. A kind the backend does
// not lower yet is reported and written as void so the output still
// reads.
func (e *Emitter) irType(t types.Type, pos token.Pos) string {
	switch t := e.sub(t).(type) {
	case *types.Basic:
		if s := basicType(t); s != "" {
			return s
		}
	case *types.Named:
		switch t.Underlying().(type) {
		case *types.Struct, *types.Enum:
			return e.defineNamed(t, pos)
		}

		return e.irType(t.Underlying(), pos)
	case *types.Ref:
		if view(t) {
			return e.irType(t.Elem(), pos)
		}

		return "ptr"
	case *types.Pointer, *types.Chan:
		return "ptr"
	case *types.Option:
		if nullable(t) {
			return "ptr"
		}

		return "{ i1, " + e.irType(t.Elem(), pos) + " }"
	case *types.Result:
		return "{ i1, [" + strconv.FormatInt(e.payloadWords(t.Val(), t.Err()), 10) + " x i64] }"
	case *types.Slice:
		return "%sn_slice"
	case *types.Array:
		return "[" + strconv.FormatInt(t.Len(), 10) + " x " + e.irType(t.Elem(), pos) + "]"
	case *types.Map:
		return "%sn_map"
	case *types.Tuple:
		if t == types.Unit {
			return "{}"
		}

		parts := make([]string, t.Len())
		for i := range parts {
			parts[i] = e.irType(t.At(i), pos)
		}

		return "{ " + strings.Join(parts, ", ") + " }"
	case *types.Signature:
		return "{ ptr, [" + strconv.Itoa(e.boxWords) + " x i64] }"
	case *types.Trait:
		return "%sn_trait"
	case *types.TypeParam:
		if s := e.sub(t); s != t {
			return e.irType(s, pos)
		}
	}

	e.unsupported(pos, "values of type "+t.String())
	return "void"
}

// view reports whether a borrow is the three words of the string or
// slice it lends rather than the address of a place: a ref borrow of
// a string or a slice, which x[lo:hi] builds with no place behind it.
// A mut borrow is the address, since it is written through.
func view(t *types.Ref) bool {
	return !t.Mut() && viewed(t.Elem())
}

// viewed reports whether a ref borrow of a value of type t is a view:
// whether t is a string or a slice.
func viewed(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return isString(u)
	case *types.Slice:
		return true
	}

	return false
}

// nullable reports whether an option is the pointer itself: an
// option of an owned pointer or of a mut borrow, which are never
// null, so null is free to mean None.
func nullable(t *types.Option) bool {
	switch u := t.Elem().(type) {
	case *types.Pointer:
		return true
	case *types.Ref:
		return u.Mut()
	}

	return false
}

// defineNamed writes the named type of a declared struct or enum once
// and returns its name. A struct is its fields in order, with a flag
// after them when the type has a Drop method, saying the value is
// live; an enum is a tag and its payload words.
func (e *Emitter) defineNamed(t *types.Named, pos token.Pos) string {
	name := "%" + lower.NamedName(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	var body string
	switch u := t.Underlying().(type) {
	case *types.Struct:
		parts := make([]string, 0, u.NumFields()+1)
		for i := 0; i < u.NumFields(); i++ {
			parts = append(parts, e.irType(u.Field(i).Type(), pos))
		}

		if hasDrop(t) {
			parts = append(parts, "i1")
		}

		body = "{ " + strings.Join(parts, ", ") + " }"
	case *types.Enum:
		var fields [][]types.Type
		for i := 0; i < u.NumVariants(); i++ {
			var ts []types.Type
			for _, f := range u.Variant(i).Fields() {
				ts = append(ts, f.Type())
			}

			fields = append(fields, ts)
		}

		body = "{ i32, [" + strconv.FormatInt(e.enumWords(fields), 10) + " x i64] }"
		if hasDrop(t) {
			body = "{ i32, [" + strconv.FormatInt(e.enumWords(fields), 10) + " x i64], i1 }"
		}
	}

	put(&e.types, name, " = type ", body, "\n")
	return name
}

// variantType writes the struct type of a variant's fields, read and
// written at the payload's address.
func (e *Emitter) variantType(fields []*types.Var, pos token.Pos) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = e.irType(f.Type(), pos)
	}

	return "{ " + strings.Join(parts, ", ") + " }"
}

// payloadWords is the number of words a result's payload needs for
// its value or its error.
func (e *Emitter) payloadWords(ts ...types.Type) int64 {
	var n int64
	for _, t := range ts {
		n = max(n, (e.size(t)+7)/8)
	}

	return n
}

// enumWords is the number of words an enum's payload needs for the
// fields of its largest variant.
func (e *Emitter) enumWords(variants [][]types.Type) int64 {
	var n int64
	for _, fields := range variants {
		n = max(n, (e.structSize(fields)+7)/8)
	}

	return n
}

// size is the number of bytes a value of t takes in memory, under
// the natural alignment the IR's data layout gives every type.
func (e *Emitter) size(t types.Type) int64 {
	switch t := t.(type) {
	case *types.Basic:
		switch t.Kind() {
		case types.String, types.Error:
			return 24
		case types.Bool, types.Int8, types.Uint8, types.Local:
			return 1
		case types.Int16, types.Uint16:
			return 2
		case types.Int32, types.Uint32, types.Float32:
			return 4
		}

		return 8
	case *types.Named:
		switch u := t.Underlying().(type) {
		case *types.Struct:
			var fields []types.Type
			for i := 0; i < u.NumFields(); i++ {
				fields = append(fields, u.Field(i).Type())
			}

			if hasDrop(t) {
				fields = append(fields, types.Typ[types.Bool])
			}

			return e.structSize(fields)
		case *types.Enum:
			var variants [][]types.Type
			for i := 0; i < u.NumVariants(); i++ {
				var ts []types.Type
				for _, f := range u.Variant(i).Fields() {
					ts = append(ts, f.Type())
				}

				variants = append(variants, ts)
			}

			n := 8 + 8*e.enumWords(variants)
			if hasDrop(t) {
				n += 8
			}

			return n
		}

		return e.size(t.Underlying())
	case *types.Ref:
		if view(t) {
			return e.size(t.Elem())
		}

		return 8
	case *types.Pointer, *types.Chan:
		return 8
	case *types.Option:
		if nullable(t) {
			return 8
		}

		return e.structSize([]types.Type{types.Typ[types.Bool], t.Elem()})
	case *types.Result:
		return 8 + 8*e.payloadWords(t.Val(), t.Err())
	case *types.Slice:
		return 24
	case *types.Array:
		return t.Len() * e.size(t.Elem())
	case *types.Map:
		return 64
	case *types.Tuple:
		var parts []types.Type
		for i := 0; i < t.Len(); i++ {
			parts = append(parts, t.At(i))
		}

		return e.structSize(parts)
	case *types.Signature:
		return 8 + 8*int64(e.boxWords)
	case *types.Trait:
		return 16
	case *types.TypeParam:
		if s := e.sub(t); s != t {
			return e.size(s)
		}
	}

	return 8
}

// align is the alignment of a value of t: that of its widest part.
func (e *Emitter) align(t types.Type) int64 {
	switch t := t.(type) {
	case *types.Basic:
		return min(e.size(t), 8)
	case *types.Named:
		switch u := t.Underlying().(type) {
		case *types.Struct:
			var a int64 = 1
			for i := 0; i < u.NumFields(); i++ {
				a = max(a, e.align(u.Field(i).Type()))
			}

			return a
		case *types.Enum:
			return 8
		}

		return e.align(t.Underlying())
	case *types.Option:
		if nullable(t) {
			return 8
		}

		return e.align(t.Elem())
	case *types.Array:
		return e.align(t.Elem())
	case *types.Tuple:
		var a int64 = 1
		for i := 0; i < t.Len(); i++ {
			a = max(a, e.align(t.At(i)))
		}

		return a
	case *types.TypeParam:
		if s := e.sub(t); s != t {
			return e.align(s)
		}
	}

	return 8
}

// structSize lays out fields as the IR does, each at its alignment,
// and rounds the whole to the largest alignment.
func (e *Emitter) structSize(fields []types.Type) int64 {
	var n, a int64 = 0, 1
	for _, f := range fields {
		fa := e.align(f)
		a = max(a, fa)
		n = (n + fa - 1) / fa * fa
		n += e.size(f)
	}

	return (n + a - 1) / a * a
}

// basicType writes the IR type of a basic kind, or nothing for a kind
// the backend does not lower yet. An error is its message for now, as
// in the C backend; a bool is i1, which the IR widens in memory.
func basicType(t *types.Basic) string {
	switch t.Kind() {
	case types.Bool:
		return "i1"
	case types.String, types.Error:
		return "%sn_string"
	case types.Int8, types.Uint8, types.Local:
		return "i8"
	case types.Int16, types.Uint16:
		return "i16"
	case types.Int32, types.Uint32:
		return "i32"
	case types.Int64, types.Uint64, types.Int, types.Uint:
		return "i64"
	case types.Float32:
		return "float"
	case types.Float64:
		return "double"
	}

	return ""
}

// basic returns the basic type under t, or nil.
func basic(t types.Type) *types.Basic {
	b, _ := t.Underlying().(*types.Basic)
	return b
}

// isFloat reports whether t is a floating-point number.
func isFloat(t types.Type) bool {
	b := basic(t)
	return b != nil && b.Info()&types.IsFloat != 0
}

// isUnsigned reports whether t is an unsigned integer.
func isUnsigned(t types.Type) bool {
	b := basic(t)
	return b != nil && b.Info()&types.IsUnsigned != 0
}

// isInteger reports whether t is an integer of either sign.
func isInteger(t types.Type) bool {
	b := basic(t)
	return b != nil && b.Info()&types.IsInteger != 0
}

// isBool reports whether t is bool.
func isBool(t types.Type) bool {
	b := basic(t)
	return b != nil && b.Kind() == types.Bool
}

// isString reports whether t is a string, or an error, which is kept
// as its message.
func isString(t types.Type) bool {
	b := basic(t)
	return b != nil && (b.Kind() == types.String || b.Kind() == types.Error)
}

// isNever reports whether t is the type of a branch that leaves.
func isNever(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.Never
}

// bits is the width of a number type.
func bits(t types.Type) int {
	switch basic(t).Kind() {
	case types.Int8, types.Uint8:
		return 8
	case types.Int16, types.Uint16:
		return 16
	case types.Int32, types.Uint32, types.Float32:
		return 32
	}

	return 64
}

// zero writes the zero constant of an IR type: what a place holds
// once its value moved away, and what var x T starts as.
func zero(t string) string {
	switch {
	case t == "i1":
		return "false"
	case t == "float" || t == "double":
		return "0x0000000000000000"
	case t == "ptr":
		return "null"
	case strings.HasPrefix(t, "i"):
		return "0"
	}

	return "zeroinitializer"
}
