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

// Some operations on a composite type need a function of their own in
// C: turning a value into text, and comparing two values. They are
// generated on first use, one per type, under the runtime's prefix.

// stringer returns the name of the function that turns a value of a
// composite type into text, writing it on first use: a struct as
// {field field}, or its name when it has no fields; an enum as
// Name(field, field); an option as Some(x) or None; a Result as Ok(x)
// or Err(e); a tuple as (a, b); a slice as [a b]. A string inside a
// constructor is quoted, so the text reads as the value would be
// written; inside a struct, tuple or slice it is not.
func (e *Emitter) stringer(t types.Type, pos token.Pos) string {
	name := "tk_str_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	e.protos.WriteString("static tk_string " + name + "(" + e.cType(t, pos) + " v);\n")
	var b strings.Builder
	b.WriteString("static tk_string " + name + "(" + e.cType(t, pos) + " v) {\n\t(void)v; // () and an empty struct have nothing to read\n")
	v := deref("v", t)
	switch u := valueType(t).Underlying().(type) {
	case *types.Struct:
		if u.NumFields() == 0 {
			b.WriteString("\treturn " + literal(valueType(t).String()) + ";\n")
			break
		}

		b.WriteString("\treturn " + joined("{", "}", " ", e.structParts(u, v, pos)) + ";\n")
	case *types.Enum:
		e.enumStringer(&b, u, v, pos)
	case *types.Option:
		b.WriteString("\tif (!" + v + ".has) {\n\t\treturn " + literal("None") + ";\n\t}\n\n")
		b.WriteString("\treturn " + joined("Some(", ")", "", []string{e.toText(v+".val", u.Elem(), pos, true)}) + ";\n")
	case *types.Result:
		b.WriteString("\tif (!" + v + ".ok) {\n\t\treturn " + joined("Err(", ")", "", []string{e.toText(v+".err", u.Err(), pos, true)}) + ";\n\t}\n\n")
		b.WriteString("\treturn " + joined("Ok(", ")", "", []string{e.toText(v+".val", u.Val(), pos, true)}) + ";\n")
	case *types.Tuple:
		parts := make([]string, u.Len())
		for i := range parts {
			parts[i] = e.toText(v+".e"+strconv.Itoa(i), u.At(i), pos, false)
		}

		b.WriteString("\treturn " + joined("(", ")", ", ", parts) + ";\n")
	case *types.Map:
		k, val := "(("+e.cType(u.Key(), pos)+" *)"+v+".keys)[i]", "(("+e.cType(u.Value(), pos)+" *)"+v+".vals)[i]"
		b.WriteString("\ttk_string out = " + literal("map[") + ";\n")
		b.WriteString("\tfor (int64_t i = tk_map_next(&" + v + ", 0); i >= 0; i = tk_map_next(&" + v + ", i + 1)) {\n")
		b.WriteString("\t\tif (out.len > 4) {\n\t\t\tout = tk_concat(out, " + literal(" ") + ");\n\t\t}\n\n")
		b.WriteString("\t\tout = tk_concat(tk_concat(tk_concat(out, " + e.toText(k, u.Key(), pos, false) + "), " + literal(":") + "), " + e.toText(val, u.Value(), pos, false) + ");\n\t}\n\n")
		b.WriteString("\treturn tk_concat(out, " + literal("]") + ");\n")
	case *types.Slice:
		b.WriteString("\ttk_string out = " + literal("[") + ";\n")
		b.WriteString("\tfor (int64_t i = 0; i < " + v + ".len; i++) {\n")
		b.WriteString("\t\tif (i > 0) {\n\t\t\tout = tk_concat(out, " + literal(" ") + ");\n\t\t}\n\n")
		b.WriteString("\t\tout = tk_concat(out, " + e.toText(v+".ptr[i]", u.Elem(), pos, false) + ");\n\t}\n\n")
		b.WriteString("\treturn tk_concat(out, " + literal("]") + ");\n")
	default:
		b.WriteString("\treturn " + e.toText(v, valueType(t), pos, false) + ";\n")
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
			parts[j] = e.toText(v+"."+variantMember(variant)+"."+fieldName(f, j), f.Type(), pos, true)
		}

		b.WriteString("\t\treturn " + joined(variant.Name()+"(", ")", ", ", parts) + ";\n")
	}

	b.WriteString("\t}\n\n\treturn " + literal("?") + ";\n")
}

func (e *Emitter) structParts(s *types.Struct, v string, pos token.Pos) []string {
	parts := make([]string, s.NumFields())
	for i := range parts {
		f := s.Field(i)
		parts[i] = e.toText(v+"."+ident(f.Name()), f.Type(), pos, false)
	}

	return parts
}

// toText writes the expression that turns the C value v of type t
// into a tk_string; quoted says whether a string is written in quotes.
func (e *Emitter) toText(v string, t types.Type, pos token.Pos, quoted bool) string {
	b, ok := valueType(t).Underlying().(*types.Basic)
	if !ok {
		return e.stringer(t, pos) + "(" + v + ")"
	}

	v = deref(v, t)
	switch {
	case isString(b) && quoted:
		return "tk_quote(" + v + ")"
	case isString(b):
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

// equal writes the test that two C values of type t are equal: the
// operator for numbers and booleans, without parentheses so that an
// if around it has one pair, the runtime for strings, and a generated
// function for anything composite.
func (e *Emitter) equal(a, b string, t types.Type, pos token.Pos) string {
	t = valueType(t)
	if basic, ok := t.Underlying().(*types.Basic); ok {
		if isString(basic) {
			return "tk_string_eq(" + a + ", " + b + ")"
		}

		return a + " == " + b
	}

	return e.comparer(t, pos) + "(" + a + ", " + b + ")"
}

// comparer returns the name of the function comparing two values of a
// composite type part by part, writing it on first use.
func (e *Emitter) comparer(t types.Type, pos token.Pos) string {
	name := "tk_eq_" + mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct := e.cType(t, pos)
	e.protos.WriteString("static bool " + name + "(" + ct + " a, " + ct + " b);\n")
	var b strings.Builder
	b.WriteString("static bool " + name + "(" + ct + " a, " + ct + " b) {\n")
	switch u := t.Underlying().(type) {
	case *types.Struct:
		var parts []string
		for i := 0; i < u.NumFields(); i++ {
			f := ident(u.Field(i).Name())
			parts = append(parts, e.equal("a."+f, "b."+f, u.Field(i).Type(), pos))
		}

		b.WriteString("\treturn " + allOf(parts) + ";\n")
	case *types.Enum:
		b.WriteString("\tif (a.tag != b.tag) {\n\t\treturn false;\n\t}\n\n\tswitch (a.tag) {\n")
		for i := 0; i < u.NumVariants(); i++ {
			v := u.Variant(i)
			if len(v.Fields()) == 0 {
				continue
			}

			var parts []string
			for j, f := range v.Fields() {
				field := "." + variantMember(v) + "." + fieldName(f, j)
				parts = append(parts, e.equal("a"+field, "b"+field, f.Type(), pos))
			}

			b.WriteString("\tcase " + strconv.Itoa(i) + ":\n\t\treturn " + allOf(parts) + ";\n")
		}

		b.WriteString("\t}\n\n\treturn true;\n")
	case *types.Option:
		b.WriteString("\treturn a.has == b.has && (!a.has || " + e.equal("a.val", "b.val", u.Elem(), pos) + ");\n")
	case *types.Result:
		b.WriteString("\tif (a.ok != b.ok) {\n\t\treturn false;\n\t}\n\n")
		b.WriteString("\treturn a.ok ? " + e.equal("a.val", "b.val", u.Val(), pos) + " : " + e.equal("a.err", "b.err", u.Err(), pos) + ";\n")
	case *types.Tuple:
		var parts []string
		for i := 0; i < u.Len(); i++ {
			f := ".e" + strconv.Itoa(i)
			parts = append(parts, e.equal("a"+f, "b"+f, u.At(i), pos))
		}

		b.WriteString("\treturn " + allOf(parts) + ";\n")
	case *types.Pointer:
		b.WriteString("\treturn " + e.equal("(*a)", "(*b)", u.Elem(), pos) + ";\n")
	case *types.Slice:
		b.WriteString("\tif (a.len != b.len) {\n\t\treturn false;\n\t}\n\n")
		b.WriteString("\tfor (int64_t i = 0; i < a.len; i++) {\n\t\tif (!" + e.equal("a.ptr[i]", "b.ptr[i]", u.Elem(), pos) + ") {\n\t\t\treturn false;\n\t\t}\n\t}\n\n\treturn true;\n")
	default:
		e.unsupported(pos, "comparing values of type "+t.String())
		b.WriteString("\treturn false;\n")
	}

	b.WriteString("}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

// mapType returns the name of the descriptor the runtime needs for a
// map type, its key and value sizes and the key's hash and equality,
// writing it and the two key functions on first use.
func (e *Emitter) mapType(t *types.Map, pos token.Pos) string {
	name := "tk_mt_" + mangle(t)
	if e.state[name] != undeclared {
		return "&" + name
	}

	e.state[name] = defined
	key := e.cType(t.Key(), pos)
	hash, eq := "tk_khash_"+mangle(t.Key()), "tk_keq_"+mangle(t.Key())
	if e.state[hash] == undeclared {
		e.state[hash] = defined
		e.helpers.WriteString("static uint64_t " + hash + "(const void *p) {\n\t" + key + " k = *(const " + key + " *)p;\n\t(void)k;\n\treturn " + e.hash("k", t.Key(), pos) + ";\n}\n\n")
		e.helpers.WriteString("static bool " + eq + "(const void *a, const void *b) {\n\treturn " + e.equal("(*(const "+key+" *)a)", "(*(const "+key+" *)b)", t.Key(), pos) + ";\n}\n\n")
	}

	e.helpers.WriteString("static const tk_map_type " + name + " = {sizeof(" + key + "), sizeof(" + e.cType(t.Value(), pos) + "), " + hash + ", " + eq + "};\n\n")
	return "&" + name
}

// hash writes the hash of the C value v of key type t: bytes for a
// string, the value for a number, and a mix of the parts for a tuple,
// struct, option or pointer.
func (e *Emitter) hash(v string, t types.Type, pos token.Pos) string {
	v = deref(v, t)
	switch u := valueType(t).Underlying().(type) {
	case *types.Basic:
		switch {
		case isString(u):
			return "tk_hash_bytes(" + v + ".ptr, (size_t)" + v + ".len)"
		case u.Info()&types.IsFloat != 0:
			return "(" + v + " == 0 ? 0 : tk_hash_bytes(&" + v + ", sizeof " + v + "))" // 0.0 and -0.0 are equal, so they hash alike
		}

		return "tk_hash_mix(0, (uint64_t)" + v + ")"
	case *types.Option:
		return "(" + v + ".has ? tk_hash_mix(1, " + e.hash(v+".val", u.Elem(), pos) + ") : 0)"
	case *types.Tuple:
		h := "0"
		for i := 0; i < u.Len(); i++ {
			h = "tk_hash_mix(" + h + ", " + e.hash(v+".e"+strconv.Itoa(i), u.At(i), pos) + ")"
		}

		return h
	case *types.Struct:
		h := "0"
		for i := 0; i < u.NumFields(); i++ {
			h = "tk_hash_mix(" + h + ", " + e.hash(v+"."+ident(u.Field(i).Name()), u.Field(i).Type(), pos) + ")"
		}

		return h
	}

	e.unsupported(pos, "map keys of type "+t.String())
	return "0"
}

// allOf joins conditions with &&; none is true.
func allOf(parts []string) string {
	if len(parts) == 0 {
		return "true"
	}

	return strings.Join(parts, " && ")
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
