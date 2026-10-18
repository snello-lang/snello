// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
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

// cType writes the C type for a Teak type and makes sure the type is
// complete, so a value of it can be declared. A kind the backend does
// not lower yet is reported and written as void so the output still
// reads.
func (e *Emitter) cType(t types.Type, pos token.Pos) string {
	e.define(t, pos)
	return e.typeName(t, pos)
}

// typeName writes the C name of a Teak type, declaring it when it is a
// struct the output has not seen; the type may still be incomplete,
// which is all a pointer to it needs.
func (e *Emitter) typeName(t types.Type, pos token.Pos) string {
	switch t := t.(type) {
	case *types.Basic:
		return basicType(t)
	case *types.Named:
		return ident(t.Obj().Name())
	case *types.Ref:
		if t.Mut() {
			return e.typeName(t.Elem(), pos) + " *"
		}

		return e.typeName(t.Elem(), pos)
	case *types.Pointer:
		return e.typeName(t.Elem(), pos) + " *"
	case *types.Option, *types.Result:
		return e.declare(t, pos)
	case *types.Tuple:
		if t == types.Unit {
			return "tk_unit"
		}

		return e.declare(t, pos)
	}

	e.unsupported(pos, "values of type "+t.String())
	return "void"
}

// declare writes the forward declaration of a synthesized struct type
// once and returns its name.
func (e *Emitter) declare(t types.Type, pos token.Pos) string {
	name := "tk_" + mangle(t)
	if e.state[name] == undeclared {
		e.state[name] = declared
		e.decls.WriteString("typedef struct " + name + " " + name + ";\n")
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
	case *types.Tuple:
		if t == types.Unit {
			return
		}

		e.defineStruct(t, pos, func() string {
			var b strings.Builder
			for i := 0; i < t.Len(); i++ {
				b.WriteString("\t" + e.cType(t.At(i), pos) + " e" + strconv.Itoa(i) + ";\n")
			}

			return b.String()
		})
	}
}

// defineNamed writes a declared type: a struct or enum as a C struct
// whose forward declaration Emit wrote, any other as a typedef of its
// underlying type.
func (e *Emitter) defineNamed(t *types.Named, pos token.Pos) {
	name := ident(t.Obj().Name())
	switch u := t.Underlying().(type) {
	case *types.Struct:
		e.defineStruct(t, pos, func() string { return e.structFields(u, pos) })
	case *types.Enum:
		e.defineStruct(t, pos, func() string { return e.enumFields(u, pos) })
	default:
		if e.state[name] != undeclared {
			return
		}

		e.state[name] = defined
		e.types.WriteString("typedef " + e.cType(u, pos) + " " + name + ";\n")
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
	e.types.WriteString("struct " + name + " {\n" + body + "};\n\n")
}

func (e *Emitter) structFields(s *types.Struct, pos token.Pos) string {
	if s.NumFields() == 0 {
		return "\tchar tk_pad;\n"
	}

	var b strings.Builder
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		b.WriteString("\t" + e.cType(f.Type(), pos) + " " + ident(f.Name()) + ";\n")
	}

	return b.String()
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
			payloads.WriteString("\t\t\t" + e.cType(f.Type(), pos) + " " + fieldName(f, j) + ";\n")
		}

		payloads.WriteString("\t\t} " + variantMember(v) + ";\n")
	}

	if payloads.Len() > 0 {
		b.WriteString("\tunion {\n" + payloads.String() + "\t};\n")
	}

	return b.String()
}

// fieldName is the C name of a variant's field: its own name, or its
// position when the variant declares it without one.
func fieldName(f *types.Var, i int) string {
	if f.Name() == "" {
		return "f" + strconv.Itoa(i)
	}

	return ident(f.Name())
}

// variantMember is the name of a variant's payload inside the union,
// which must not collide with the tag beside it.
func variantMember(v *types.Variant) string {
	if v.Name() == "tag" {
		return "tag_"
	}

	return ident(v.Name())
}

// mangle writes a type as a C identifier fragment.
func mangle(t types.Type) string {
	switch t := t.(type) {
	case *types.Basic:
		return types.Typ[t.Kind()].Name()
	case *types.Named:
		return ident(t.Obj().Name())
	case *types.Ref:
		if t.Mut() {
			return "mut_" + mangle(t.Elem())
		}

		return "ref_" + mangle(t.Elem())
	case *types.Pointer:
		return "ptr_" + mangle(t.Elem())
	case *types.Option:
		return "opt_" + mangle(t.Elem())
	case *types.Result:
		return "res_" + mangle(t.Val()) + "_" + mangle(t.Err())
	case *types.Tuple:
		if t == types.Unit {
			return "unit"
		}

		parts := make([]string, t.Len())
		for i := range parts {
			parts[i] = mangle(t.At(i))
		}

		return "tup" + strconv.Itoa(t.Len()) + "_" + strings.Join(parts, "_")
	}

	return "unknown"
}

// valueType strips the borrows and owned pointers in front of a type,
// as the checker does for a field access or a match.
func valueType(t types.Type) types.Type {
	for {
		switch u := t.(type) {
		case *types.Ref:
			t = u.Elem()
		case *types.Pointer:
			t = u.Elem()
		default:
			return t
		}
	}
}

// depth is the number of C pointers in front of the value of a Teak
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

// zeroValue writes the initializer of a variable declared without a
// value.
func (e *Emitter) zeroValue(t types.Type) string {
	switch valueType(t).Underlying().(type) {
	case *types.Basic:
		if isString(t) {
			return "{0}"
		}

		return "0"
	}

	if depth(t) > 0 {
		return "NULL"
	}

	return "{0}"
}

// stringer returns the name of the function that turns a value of a
// composite type into text, writing it on first use: a struct as
// {field field}, an enum as Name(field, field), an option as Some(x)
// or None, a Result as Ok(x) or Err(e), a tuple as (a, b).
func (e *Emitter) stringer(t types.Type, pos token.Pos) string {
	name := "tk_str_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	e.protos.WriteString("static tk_string " + name + "(" + e.cType(t, pos) + " v);\n")
	var b strings.Builder
	b.WriteString("static tk_string " + name + "(" + e.cType(t, pos) + " v) {\n\t(void)v; // the printer of () reads nothing\n")
	v := deref("v", t)
	switch u := valueType(t).Underlying().(type) {
	case *types.Struct:
		b.WriteString("\treturn " + joined("{", "}", " ", e.structParts(u, v, pos)) + ";\n")
	case *types.Enum:
		e.enumStringer(&b, u, v, pos)
	case *types.Option:
		b.WriteString("\tif (!" + v + ".has) {\n\t\treturn " + literal("None") + ";\n\t}\n\n")
		b.WriteString("\treturn " + joined("Some(", ")", "", []string{e.toText(v+".val", u.Elem(), pos)}) + ";\n")
	case *types.Result:
		b.WriteString("\tif (!" + v + ".ok) {\n\t\treturn " + joined("Err(", ")", "", []string{e.toText(v+".err", u.Err(), pos)}) + ";\n\t}\n\n")
		b.WriteString("\treturn " + joined("Ok(", ")", "", []string{e.toText(v+".val", u.Val(), pos)}) + ";\n")
	case *types.Tuple:
		parts := make([]string, u.Len())
		for i := range parts {
			parts[i] = e.toText(v+".e"+strconv.Itoa(i), u.At(i), pos)
		}

		b.WriteString("\treturn " + joined("(", ")", ", ", parts) + ";\n")
	default:
		b.WriteString("\treturn " + e.toText(v, valueType(t), pos) + ";\n")
	}

	b.WriteString("}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

func (e *Emitter) enumStringer(b *strings.Builder, en *types.Enum, v string, pos token.Pos) {
	b.WriteString("\tswitch (" + v + ".tag) {\n")
	for i := 0; i < en.NumVariants(); i++ {
		variant := en.Variant(i)
		b.WriteString("\tcase " + strconv.Itoa(i) + ":\n")
		if len(variant.Fields()) == 0 {
			b.WriteString("\t\treturn " + literal(variant.Name()) + ";\n")
			continue
		}

		parts := make([]string, len(variant.Fields()))
		for j, f := range variant.Fields() {
			parts[j] = e.toText(v+"."+variantMember(variant)+"."+fieldName(f, j), f.Type(), pos)
		}

		b.WriteString("\t\treturn " + joined(variant.Name()+"(", ")", ", ", parts) + ";\n")
	}

	b.WriteString("\t}\n\n\treturn " + literal("?") + ";\n")
}

func (e *Emitter) structParts(s *types.Struct, v string, pos token.Pos) []string {
	parts := make([]string, s.NumFields())
	for i := range parts {
		f := s.Field(i)
		parts[i] = e.toText(v+"."+ident(f.Name()), f.Type(), pos)
	}

	return parts
}

// toText writes the expression that turns the C value v of type t
// into a tk_string.
func (e *Emitter) toText(v string, t types.Type, pos token.Pos) string {
	b, ok := valueType(t).Underlying().(*types.Basic)
	if !ok {
		return e.stringer(t, pos) + "(" + v + ")"
	}

	v = deref(v, t)
	switch {
	case b.Kind() == types.String:
		return v
	case b.Kind() == types.Bool:
		return "tk_bool_to_string(" + v + ")"
	case b.Info()&types.IsFloat != 0:
		return "tk_f64_to_string((double)(" + v + "))"
	case b.Info()&types.IsUnsigned != 0:
		return "tk_uint_to_string((uint64_t)(" + v + "))"
	case b.Info()&types.IsInteger != 0:
		return "tk_int_to_string((int64_t)(" + v + "))"
	}

	e.unsupported(pos, "printing values of type "+t.String())
	return literal("?")
}

// joined writes the concatenation of open, the parts separated by sep,
// and close.
func joined(open, close, sep string, parts []string) string {
	out := literal(open)
	for i, p := range parts {
		if i > 0 && sep != "" {
			out = concat(out, literal(sep))
		}

		out = concat(out, p)
	}

	return concat(out, literal(close))
}

// deref writes v, a C value of Teak type t, dereferenced to the value.
func deref(v string, t types.Type) string {
	for d := depth(t); d > 0; d-- {
		v = "(*" + v + ")"
	}

	return v
}

// literal writes a tk_string for constant text.
func literal(s string) string {
	return "TK_STR(" + cString(s) + ", " + strconv.Itoa(len(s)) + ")"
}
