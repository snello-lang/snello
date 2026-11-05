// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The state of a C type definition, kept per C type name: C needs a
// type complete before a value of it is declared, and only declared
// before a pointer to it is.
const (
	undeclared = iota
	declared
	defining
	defined
)

// zeroValue is the initializer of a variable declared without a value:
// {0} zeroes a number, a pointer and every field of a struct alike, and
// makes a compound literal with (T) in front.
const zeroValue = "{0}"

// cType writes the C type for a Snello type and makes sure the type is
// complete, so a value of it can be declared. A kind the backend does
// not lower yet is reported and written as void so the output still
// reads.
func (e *Emitter) cType(t types.Type, pos token.Pos) string {
	e.define(t, pos)
	return e.typeName(t, pos)
}

// typeName writes the C name of a Snello type, declaring it when it is a
// struct the output has not seen; the type may still be incomplete,
// which is all a pointer to it needs.
func (e *Emitter) typeName(t types.Type, pos token.Pos) string {
	switch t := t.(type) {
	case *types.Basic:
		return basicType(t)
	case *types.Named:
		name := lower.NamedName(t)
		if (t.TypeArgs() != nil || t.Obj().Pos() == token.NoPos) && e.state[name] == undeclared { // an instance, or the predeclared Ordering: no declaration of the program writes it
			switch t.Underlying().(type) {
			case *types.Struct, *types.Enum:
				e.state[name] = declared // the forward declaration typeDecl writes for a declared type
				put(&e.decls, "typedef struct ", name, " ", name, ";\n")
			}
		}

		return name
	case *types.Ref:
		if t.Mut() {
			return e.typeName(t.Elem(), pos) + " *"
		}

		return e.typeName(t.Elem(), pos)
	case *types.Pointer:
		return e.typeName(t.Elem(), pos) + " *"
	case *types.Chan:
		return "sn_chan *"
	case *types.Option, *types.Result, *types.Slice, *types.Array, *types.Signature:
		return e.declare(t)
	case *types.Map:
		name := "sn_" + lower.Mangle(t)
		if e.state[name] == undeclared {
			e.state[name] = defined
			put(&e.decls, "typedef sn_map ", name, ";\n")
		}

		return name
	case *types.Tuple:
		if t == types.Unit {
			return "sn_unit"
		}

		return e.declare(t)
	case *types.Trait:
		name := lower.Ident(t.Obj().Name())
		if e.state[name] == undeclared {
			e.state[name] = declared
			tab := tableType(t)
			put(&e.decls, "typedef struct ", name, " ", name, ";\ntypedef struct ", tab, " ", tab, ";\n")
		}

		return name
	}

	e.unsupported(pos, "values of type "+t.String())
	return "void"
}

// declare writes the forward declaration of a synthesized struct type
// once and returns its name.
func (e *Emitter) declare(t types.Type) string {
	name := "sn_" + lower.Mangle(t)
	if e.state[name] == undeclared {
		e.state[name] = declared
		put(&e.decls, "typedef struct ", name, " ", name, ";\n")
	}

	return name
}

// define writes the definition of a type, after the definitions of the
// types its values hold by value; a type reached only through a pointer
// is declared, not defined, which is what lets a type refer to itself.
func (e *Emitter) define(t types.Type, pos token.Pos) {
	switch t := t.(type) {
	case *types.Named:
		e.defineNamed(t, pos)
	case *types.Ref:
		if t.Mut() {
			e.typeName(t.Elem(), pos)
		} else {
			e.define(t.Elem(), pos)
		}
	case *types.Pointer:
		e.typeName(t.Elem(), pos)
	case *types.Option:
		e.defineStruct(t, pos, func() string {
			return "\tbool has;\n\t" + e.cType(t.Elem(), pos) + " val;\n"
		})
	case *types.Result:
		e.defineStruct(t, pos, func() string {
			return "\tbool ok;\n\tunion {\n\t\t" + e.cType(t.Val(), pos) + " val;\n\t\t" + e.cType(t.Err(), pos) + " err;\n\t};\n"
		})
	case *types.Slice:
		e.defineStruct(t, pos, func() string {
			return "\t" + e.typeName(t.Elem(), pos) + " *ptr;\n\tsn_int len;\n\tsn_int cap;\n"
		})
	case *types.Array: // a struct, so that it is copied and passed as a value; the element is defined whole, since it is inside
		e.defineStruct(t, pos, func() string {
			return "\t" + e.cType(t.Elem(), pos) + " at[" + strconv.FormatInt(t.Len(), 10) + "];\n"
		})
	case *types.Signature:
		e.defineStruct(t, pos, func() string {
			return "\t" + e.resultType(t, pos) + " (*fn)(" + e.closureParams(t, pos) + ");\n\tuint64_t box[SN_BOX_WORDS];\n"
		})
	case *types.Tuple:
		if t == types.Unit {
			return
		}

		e.defineStruct(t, pos, func() string {
			var b strings.Builder
			for i := 0; i < t.Len(); i++ {
				put(&b, "\t", e.cType(t.At(i), pos), " e", strconv.Itoa(i), ";\n")
			}

			return b.String()
		})
	case *types.Trait:
		e.defineTrait(t, pos)
	}
}

// defineNamed writes a declared type: a struct or enum as a C struct
// whose forward declaration Emit wrote, any other as a typedef of its
// underlying type.
func (e *Emitter) defineNamed(t *types.Named, pos token.Pos) {
	name := lower.NamedName(t)
	switch u := t.Underlying().(type) {
	case *types.Struct:
		e.defineStruct(t, pos, func() string { return e.structFields(u, pos) + liveFlag(t) })
	case *types.Enum:
		e.defineStruct(t, pos, func() string { return e.enumFields(u, pos) + liveFlag(t) })
	default:
		if e.state[name] != undeclared {
			return
		}

		e.state[name] = defined
		put(&e.types, "typedef ", e.cType(u, pos), " ", name, ";\n")
	}
}

// defineStruct writes struct name { fields };. The fields are built
// after the state is set to defining, so a type holding itself by value
// is caught rather than recursed into.
func (e *Emitter) defineStruct(t types.Type, pos token.Pos, fields func() string) {
	name := e.typeName(t, pos)
	switch e.state[name] {
	case defined:
		return
	case defining:
		e.unsupported(pos, "a type that holds itself by value, "+t.String()+"; hold it through a pointer")
		return
	}

	e.state[name] = defining
	body := fields()
	e.state[name] = defined
	put(&e.types, "struct ", name, " {\n", body, "};\n\n")
}

func (e *Emitter) structFields(s *types.Struct, pos token.Pos) string {
	if s.NumFields() == 0 {
		return "\tchar sn_pad;\n"
	}

	var b strings.Builder
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		put(&b, "\t", e.cType(f.Type(), pos), " ", lower.Ident(f.Name()), ";\n")
	}

	return b.String()
}

// liveFlag writes the field that says a value of a type with a Drop
// method is live: built and not moved away, so its Drop runs once.
func liveFlag(t types.Type) string {
	if hasDrop(t) {
		return "\tbool sn_live;\n"
	}

	return ""
}

// enumFields writes a tagged union: the tag is the variant's index and
// the payload of each variant with fields is a struct named after it.
func (e *Emitter) enumFields(en *types.Enum, pos token.Pos) string {
	var b strings.Builder
	b.WriteString("\tint tag;\n")
	var payloads strings.Builder
	for i := 0; i < en.NumVariants(); i++ {
		v := en.Variant(i)
		if len(v.Fields()) == 0 {
			continue
		}

		payloads.WriteString("\t\tstruct {\n")
		for j, f := range v.Fields() {
			put(&payloads, "\t\t\t", e.cType(f.Type(), pos), " ", fieldName(f, j), ";\n")
		}

		put(&payloads, "\t\t} ", variantMember(v), ";\n")
	}

	if payloads.Len() > 0 {
		put(&b, "\tunion {\n", payloads.String(), "\t};\n")
	}

	return b.String()
}

// fieldName is the C name of a variant's field: its own name, or its
// position when the variant declares it without one.
func fieldName(f *types.Var, i int) string {
	if f.Name() == "" {
		return "f" + strconv.Itoa(i)
	}

	return lower.Ident(f.Name())
}

// variantMember is the name of a variant's payload inside the union,
// which must not collide with the tag beside it.
func variantMember(v *types.Variant) string {
	if v.Name() == "tag" {
		return "tag_"
	}

	return lower.Ident(v.Name())
}

// depth is the number of C pointers in front of the value of a Snello
// type: one per mut borrow and per owned pointer, none for a ref.
func depth(t types.Type) int {
	switch t := t.(type) {
	case *types.Ref:
		if t.Mut() {
			return 1 + depth(t.Elem())
		}

		return depth(t.Elem())
	case *types.Pointer:
		return 1 + depth(t.Elem())
	}

	return 0
}

// deref writes v, a C value of Snello type t, dereferenced to the value.
func deref(v string, t types.Type) string {
	return derefTo(v, t, nil)
}

// derefTo writes v, a C value of Snello type have, read down to the
// pointer depth of want: through a mut borrow, which is a C pointer
// to the place, and through an owned pointer, which a debug build
// checks first.
func derefTo(v string, have, want types.Type) string {
	for t := have; depth(t) > depth(want); {
		switch u := t.(type) {
		case *types.Ref:
			if u.Mut() {
				v = "(*" + v + ")"
			}

			t = u.Elem()
		case *types.Pointer:
			v = "SN_DEREF(" + v + ")"
			t = u.Elem()
		}
	}

	return v
}

// literal writes a sn_string for constant text.
func literal(s string) string {
	return "SN_STR(" + cString(s) + ", " + strconv.Itoa(len(s)) + ")"
}
