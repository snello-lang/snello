// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// Some operations on a composite type need a function of their own in
// C: turning a value into text, and comparing two values. They are
// generated on first use, one per type, under the runtime's prefix.

// elements writes the body of a printer over n elements reached as
// elem, with i for the index: [a b c], as Go prints a slice or an array.
func (e *Emitter) elements(b *strings.Builder, n, elem string, t types.Type, pos token.Pos) {
	put(b, "\ttk_string out = ", literal("["), ";\n")
	put(b, "\tfor (int64_t i = 0; i < ", n, "; i++) {\n")
	put(b, "\t\tif (i > 0) {\n\t\t\tout = tk_join(out, ", literal(" "), ", 1);\n\t\t}\n\n")
	put(b, "\t\tout = ", join(text{"out", true}, e.textOf(elem, t, pos, false)).s, ";\n\t}\n\n")
	put(b, "\treturn tk_join(out, ", literal("]"), ", 1);\n")
}

// stringer returns the name of the function that turns a value of a
// composite type into text, writing it on first use: a struct as
// {field field}, or its name when it has no fields; an enum as
// Name(field, field); an option as Some(x) or None; a Result as Ok(x)
// or Err(e); a tuple as (a, b); a slice as [a b]. A string inside a
// constructor is quoted, so the text reads as the value would be
// written; inside a struct, tuple or slice it is not.
func (e *Emitter) stringer(t types.Type, pos token.Pos) string {
	name := "tk_str_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	put(&e.protos, "static tk_string ", name, "(", e.cType(t, pos), " v);\n")
	var b strings.Builder
	put(&b, "static tk_string ", name, "(", e.cType(t, pos), " v) {\n\t(void)v; // () and an empty struct have nothing to read\n")
	v := deref("v", t)
	switch u := lower.ValueType(t).Underlying().(type) {
	case *types.Struct:
		if u.NumFields() == 0 {
			put(&b, "\treturn ", literal(lower.ValueType(t).String()), ";\n")
			break
		}

		put(&b, "\treturn ", joined("{", "}", " ", e.structParts(u, v, pos)).s, ";\n")
	case *types.Enum:
		e.enumStringer(&b, u, v, pos)
	case *types.Option:
		put(&b, "\tif (!", v, ".has) {\n\t\treturn ", literal("None"), ";\n\t}\n\n")
		put(&b, "\treturn ", joined("Some(", ")", "", []text{e.textOf(v+".val", u.Elem(), pos, true)}).s, ";\n")
	case *types.Result:
		put(&b, "\tif (!", v, ".ok) {\n\t\treturn ", joined("Err(", ")", "", []text{e.textOf(v+".err", u.Err(), pos, true)}).s, ";\n\t}\n\n")
		put(&b, "\treturn ", joined("Ok(", ")", "", []text{e.textOf(v+".val", u.Val(), pos, true)}).s, ";\n")
	case *types.Tuple:
		parts := make([]text, u.Len())
		for i := range parts {
			parts[i] = e.textOf(v+".e"+strconv.Itoa(i), u.At(i), pos, false)
		}

		put(&b, "\treturn ", joined("(", ")", ", ", parts).s, ";\n")
	case *types.Map:
		k, val := "(("+e.cType(u.Key(), pos)+" *)"+v+".keys)[i]", "(("+e.cType(u.Value(), pos)+" *)"+v+".vals)[i]"
		put(&b, "\ttk_string out = ", literal("map["), ";\n")
		put(&b, "\tfor (int64_t i = tk_map_next(&", v, ", 0); i >= 0; i = tk_map_next(&", v, ", i + 1)) {\n")
		put(&b, "\t\tif (out.len > 4) {\n\t\t\tout = tk_join(out, ", literal(" "), ", 1);\n\t\t}\n\n")
		put(&b, "\t\tout = ", join(join(join(text{"out", true}, e.textOf(k, u.Key(), pos, false)), text{literal(":"), false}), e.textOf(val, u.Value(), pos, false)).s, ";\n\t}\n\n")
		put(&b, "\treturn tk_join(out, ", literal("]"), ", 1);\n")
	case *types.Slice:
		e.elements(&b, v+".len", v+".ptr[i]", u.Elem(), pos)
	case *types.Array:
		e.elements(&b, strconv.FormatInt(u.Len(), 10), v+".at[i]", u.Elem(), pos)
	case *types.Trait:
		e.unsupported(pos, "printing values of type "+t.String()) // the held value's type is not known here
		put(&b, "\treturn ", literal("?"), ";\n")
	default:
		s, _ := e.toText(v, lower.ValueType(t), pos, false)
		put(&b, "\treturn ", s, ";\n")
	}

	b.WriteString("}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

func (e *Emitter) enumStringer(b *strings.Builder, en *types.Enum, v string, pos token.Pos) {
	put(b, "\tswitch (", v, ".tag) {\n")
	for i := 0; i < en.NumVariants(); i++ {
		variant := en.Variant(i)
		put(b, "\tcase ", strconv.Itoa(i), ":\n")
		if len(variant.Fields()) == 0 {
			put(b, "\t\treturn ", literal(variant.Name()), ";\n")
			continue
		}

		parts := make([]text, len(variant.Fields()))
		for j, f := range variant.Fields() {
			parts[j] = e.textOf(v+"."+variantMember(variant)+"."+fieldName(f, j), f.Type(), pos, true)
		}

		put(b, "\t\treturn ", joined(variant.Name()+"(", ")", ", ", parts).s, ";\n")
	}

	put(b, "\t}\n\n\treturn ", literal("?"), ";\n")
}

func (e *Emitter) structParts(s *types.Struct, v string, pos token.Pos) []text {
	var parts []text
	for i := range s.NumFields() {
		f := s.Field(i)
		if lower.IsLocal(f.Type()) {
			continue // the marker that keeps a value in its task holds nothing to print
		}

		parts = append(parts, e.textOf(v+"."+lower.Ident(f.Name()), f.Type(), pos, false))
	}

	return parts
}

// A text is the C expression for a tk_string and whether the
// expression builds it, in which case whoever uses it drops it: a
// string read out of a value is only lent.
type text struct {
	s     string
	owned bool
}

// A piece is one part of a string concat builds: a literal, whose
// length is known here, or a text.
type piece struct {
	lit  string
	text text
}

// join writes the concatenation of two texts, freeing the ones built
// here once joined; the result is built here.
func join(a, b text) text {
	if a.s == "" {
		return b
	}

	flags := 0
	if a.owned {
		flags |= 1
	}

	if b.owned {
		flags |= 2
	}

	return text{"tk_join(" + a.s + ", " + b.s + ", " + strconv.Itoa(flags) + ")", true}
}

func (e *Emitter) textOf(v string, t types.Type, pos token.Pos, quoted bool) text {
	s, owned := e.toText(v, t, pos, quoted)
	return text{s, owned}
}

// toText writes the expression that turns the C value v of type t
// into a tk_string, and whether the expression builds the string;
// quoted says whether a string is written in quotes.
func (e *Emitter) toText(v string, t types.Type, pos token.Pos, quoted bool) (string, bool) {
	if n, ok := lower.ValueType(t).(*types.Named); ok && (n.Implements(types.ErrorTrait) || types.IsStringer(n)) { // an error prints as its message, as the predeclared error does, and a Stringer as its String
		method := "String"
		if n.Implements(types.ErrorTrait) {
			method = "Error"
		}

		name, _ := e.methodNameOf(nil, n, method, pos)
		msg := name + "(" + deref(v, t) + ")"
		if quoted {
			return "tk_quote_owned(" + msg + ")", true
		}

		return msg, true
	}

	b, ok := lower.ValueType(t).Underlying().(*types.Basic)
	if !ok {
		return e.stringer(t, pos) + "(" + v + ")", true
	}

	v = deref(v, t)
	switch {
	case isString(b) && quoted:
		return "tk_quote(" + v + ")", true
	case isString(b):
		return v, false
	case b.Kind() == types.Bool:
		return "tk_bool_to_string(" + v + ")", false
	case b.Kind() == types.Float32: // the fewest digits that read back as the same f32
		return "tk_f32_to_string(" + v + ")", true
	case b.Info()&types.IsFloat != 0:
		return "tk_f64_to_string((double)(" + v + "))", true
	case b.Info()&types.IsUnsigned != 0:
		return "tk_uint_to_string((uint64_t)(" + v + "))", true
	case b.Info()&types.IsInteger != 0:
		return "tk_int_to_string((int64_t)(" + v + "))", true
	}

	e.unsupported(pos, "printing values of type "+t.String())
	return literal("?"), false
}

// equal writes the test that two C values of type t are equal: the Eq
// method of a type that wrote one, the operator for numbers, booleans
// and channel handles, which are equal when they are handles on one
// channel, as Go's are, without parentheses so that an if around it
// has one pair, the runtime for strings, and a generated function for
// anything composite. A ref receiver and a ref parameter are the values in C.
func (e *Emitter) equal(a, b string, t types.Type, pos token.Pos) string {
	a, b = deref(a, t), deref(b, t)
	t = lower.ValueType(t)
	if n, ok := t.(*types.Named); ok && written(n, "Eq") { // the type says how; a derived Eq is the comparison of the parts below
		name, _ := e.methodNameOf(nil, n, "Eq", pos)
		return name + "(" + a + ", " + b + ")"
	}

	if basic, ok := t.Underlying().(*types.Basic); ok {
		if isString(basic) {
			return "tk_string_eq(" + a + ", " + b + ")"
		}

		return a + " == " + b
	}

	if _, ok := t.Underlying().(*types.Chan); ok {
		return a + " == " + b
	}

	return e.comparer(t, pos) + "(" + a + ", " + b + ")"
}

// comparer returns the name of the function comparing two values of a
// composite type part by part, writing it on first use.
func (e *Emitter) comparer(t types.Type, pos token.Pos) string {
	name := "tk_eq_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct := e.cType(t, pos)
	put(&e.protos, "static bool ", name, "(", ct, " a, ", ct, " b);\n")
	var b strings.Builder
	put(&b, "static bool ", name, "(", ct, " a, ", ct, " b) {\n")
	switch u := t.Underlying().(type) {
	case *types.Struct:
		var parts []string
		for i := 0; i < u.NumFields(); i++ {
			f := lower.Ident(u.Field(i).Name())
			parts = append(parts, e.equal("a."+f, "b."+f, u.Field(i).Type(), pos))
		}

		put(&b, "\treturn ", allOf(parts), ";\n")
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

			put(&b, "\tcase ", strconv.Itoa(i), ":\n\t\treturn ", allOf(parts), ";\n")
		}

		b.WriteString("\t}\n\n\treturn true;\n")
	case *types.Option:
		put(&b, "\treturn a.has == b.has && (!a.has || ", e.equal("a.val", "b.val", u.Elem(), pos), ");\n")
	case *types.Result:
		b.WriteString("\tif (a.ok != b.ok) {\n\t\treturn false;\n\t}\n\n")
		put(&b, "\treturn a.ok ? ", e.equal("a.val", "b.val", u.Val(), pos), " : ", e.equal("a.err", "b.err", u.Err(), pos), ";\n")
	case *types.Tuple:
		var parts []string
		for i := 0; i < u.Len(); i++ {
			f := ".e" + strconv.Itoa(i)
			parts = append(parts, e.equal("a"+f, "b"+f, u.At(i), pos))
		}

		put(&b, "\treturn ", allOf(parts), ";\n")
	case *types.Pointer:
		put(&b, "\treturn ", e.equal("(*a)", "(*b)", u.Elem(), pos), ";\n")
	case *types.Slice:
		b.WriteString("\tif (a.len != b.len) {\n\t\treturn false;\n\t}\n\n")
		put(&b, "\tfor (int64_t i = 0; i < a.len; i++) {\n\t\tif (!", e.equal("a.ptr[i]", "b.ptr[i]", u.Elem(), pos), ") {\n\t\t\treturn false;\n\t\t}\n\t}\n\n\treturn true;\n")
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
	name := "tk_mt_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return "&" + name
	}

	e.state[name] = defined
	key := e.cType(t.Key(), pos)
	hash, eq := "tk_khash_"+lower.Mangle(t.Key()), "tk_keq_"+lower.Mangle(t.Key())
	if e.state[hash] == undeclared {
		e.state[hash] = defined
		put(&e.helpers, "static uint64_t ", hash, "(const void *p) {\n\t", key, " k = *(", key, " const *)p; // const after the type, so a pointer type reads right\n\t(void)k;\n\treturn ", e.hash("k", t.Key(), pos), ";\n}\n\n")
		put(&e.helpers, "static bool ", eq, "(const void *a, const void *b) {\n\treturn ", e.equal("(*(const "+key+" *)a)", "(*(const "+key+" *)b)", t.Key(), pos), ";\n}\n\n")
	}

	put(&e.helpers, "static const tk_map_type ", name, " = {sizeof(", key, "), sizeof(", e.cType(t.Value(), pos), "), ", hash, ", ", eq, ", ", e.dropFn(t.Key(), pos), ", ", e.dropFn(t.Value(), pos), "};\n\n")
	return "&" + name
}

// hash writes the hash of the C value v of key type t: the Hash method
// of a type that wrote one, bytes for a string, the value for a number,
// and a mix of the parts for a tuple, struct, option or pointer, which
// is also what a derived Hash means.
func (e *Emitter) hash(v string, t types.Type, pos token.Pos) string {
	v = deref(v, t)
	if n, ok := lower.ValueType(t).(*types.Named); ok && written(n, "Hash") {
		name, _ := e.methodNameOf(nil, n, "Hash", pos)
		return name + "(" + v + ")"
	}

	if _, ok := lower.ValueType(t).Underlying().(*types.Enum); ok {
		return e.hasher(lower.ValueType(t), pos) + "(" + v + ")"
	}

	return e.hashParts(v, lower.ValueType(t), pos)
}

// hashParts writes the hash of the C value v of type t from its parts,
// without asking for a method: what a derived Hash is.
func (e *Emitter) hashParts(v string, t types.Type, pos token.Pos) string {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case isString(u):
			return "tk_hash_string(" + v + ")"
		case u.Info()&types.IsFloat != 0:
			return "(" + v + " == 0 ? 0 : tk_hash_bytes(&" + v + ", sizeof " + v + "))" // 0.0 and -0.0 are equal, so they hash alike
		}

		return "tk_hash_mix(0, (uint64_t)" + v + ")"
	case *types.Chan:
		return "tk_hash_mix(0, (uint64_t)(uintptr_t)" + v + ")" // the channel it is a handle on
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
			h = "tk_hash_mix(" + h + ", " + e.hash(v+"."+lower.Ident(u.Field(i).Name()), u.Field(i).Type(), pos) + ")"
		}

		return h
	}

	e.unsupported(pos, "map keys of type "+t.String())
	return "0"
}

// less writes whether the number a orders before b, both of type t and
// read without side effects. Floats take Go's order, the one sort and
// cmp.Compare use: a NaN before every other value and level with
// another NaN, so a sort with NaN in it is ordered and IsSorted agrees.
func less(a, b string, t types.Type) string {
	if !isFloat(t) {
		return "(" + a + " < " + b + ")"
	}

	return "(" + a + " < " + b + " || (" + a + " != " + a + " && " + b + " == " + b + "))"
}

// compare writes the Ordering of two C values of type t: numbers and
// strings through the runtime's comparisons, a declared type through
// its Cmp, written or derived.
func (e *Emitter) compare(a, b string, t types.Type, pos token.Pos) string {
	a, b = deref(a, t), deref(b, t)
	t = lower.ValueType(t)
	if n, ok := t.(*types.Named); ok && n.Method("Cmp") != nil {
		name, _ := e.methodNameOf(nil, n, "Cmp", pos)
		return name + "(" + a + ", " + b + ")"
	}

	if basic, ok := t.Underlying().(*types.Basic); ok {
		if isString(basic) {
			return e.order() + "(tk_string_cmp(" + a + ", " + b + "))"
		}

		return e.order() + "((" + less(b, a, t) + ") - (" + less(a, b, t) + "))"
	}

	e.unsupported(pos, "ordering values of type "+t.String())
	return "0"
}

// order returns the name of the function turning a C comparison result,
// negative, zero or positive, into an Ordering, writing it on first use.
func (e *Emitter) order() string {
	name := "tk_order"
	if e.state[name] == undeclared {
		e.state[name] = defined
		ct := e.cType(types.Ordering, token.NoPos)
		put(&e.helpers, "static ", ct, " ", name, "(int c) {\n\treturn (", ct, "){.tag = c < 0 ? 0 : c > 0 ? 2 : 1};\n}\n\n")
	}

	return name
}

// orderer returns the name of the function comparing two values of a
// declared type part by part, the derived Cmp, writing it on first use:
// the first part that differs decides, and an enum's variants are in
// declaration order, with the payloads compared when the variants
// agree.
func (e *Emitter) orderer(t types.Type, pos token.Pos) string {
	name := "tk_cmp_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct, ord := e.cType(t, pos), e.cType(types.Ordering, pos)
	put(&e.protos, "static ", ord, " ", name, "(", ct, " a, ", ct, " b);\n")
	var b strings.Builder
	put(&b, "static ", ord, " ", name, "(", ct, " a, ", ct, " b) {\n\t", ord, " r;\n")
	decide := func(indent, a, bb string, t types.Type) {
		put(&b, indent, "r = ", e.compare(a, bb, t, pos), ";\n", indent, "if (r.tag != 1) {\n", indent, "\treturn r;\n", indent, "}\n\n")
	}

	switch u := t.Underlying().(type) {
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			f := lower.Ident(u.Field(i).Name())
			decide("\t", "a."+f, "b."+f, u.Field(i).Type())
		}
	case *types.Enum:
		put(&b, "\tif (a.tag != b.tag) {\n\t\treturn ", e.order(), "(a.tag - b.tag);\n\t}\n\n")
		payloads(&b, u, "a.tag", func(field string, t types.Type) { decide("\t\t", "a"+field, "b"+field, t) })
	default:
		e.unsupported(pos, "ordering values of type "+t.String())
	}

	put(&b, "\treturn (", ord, "){.tag = 1};\n}\n\n")
	e.helpers.WriteString(b.String())
	return name
}

// hasher returns the name of the function hashing a value of a
// declared type part by part, the derived Hash, writing it on first
// use; an enum mixes its tag with the payload of its variant.
func (e *Emitter) hasher(t types.Type, pos token.Pos) string {
	name := "tk_hash_" + lower.Mangle(t)
	if e.state[name] != undeclared {
		return name
	}

	e.state[name] = defined
	ct := e.cType(t, pos)
	put(&e.protos, "static uint64_t ", name, "(", ct, " v);\n")
	var b strings.Builder
	put(&b, "static uint64_t ", name, "(", ct, " v) {\n")
	if u, ok := t.Underlying().(*types.Enum); ok {
		b.WriteString("\tuint64_t h = tk_hash_mix(0, (uint64_t)v.tag);\n")
		payloads(&b, u, "v.tag", func(field string, t types.Type) {
			put(&b, "\t\th = tk_hash_mix(h, ", e.hash("v"+field, t, pos), ");\n")
		})
		b.WriteString("\treturn h;\n}\n\n")
	} else {
		put(&b, "\treturn ", e.hashParts("v", t, pos), ";\n}\n\n")
	}

	e.helpers.WriteString(b.String())
	return name
}

// payloads writes into b a switch on tag over the variants of u that
// carry fields, calling f for each field with its member path from the
// value, ".Open.e0", and its type; a function written part by part
// handles the payload of whichever variant the value holds.
func payloads(b *strings.Builder, u *types.Enum, tag string, f func(field string, t types.Type)) {
	put(b, "\tswitch (", tag, ") {\n")
	for i := 0; i < u.NumVariants(); i++ {
		v := u.Variant(i)
		if len(v.Fields()) == 0 {
			continue
		}

		put(b, "\tcase ", strconv.Itoa(i), ":\n")
		for j, fld := range v.Fields() {
			f("."+variantMember(v)+"."+fieldName(fld, j), fld.Type())
		}

		b.WriteString("\t\tbreak;\n")
	}

	b.WriteString("\t}\n\n")
}

// written reports whether the declared type has a method of its own
// under name, declared rather than derived from its fields.
func written(n *types.Named, name string) bool {
	fn := n.Method(name)
	return fn != nil && !fn.Derived()
}

// allOf joins conditions with &&; none is true.
func allOf(parts []string) string {
	if len(parts) == 0 {
		return "true"
	}

	return strings.Join(parts, " && ")
}

// joined writes the concatenation of open, the parts separated by sep,
// and close, freeing the parts built on the way.
func joined(open, close, sep string, parts []text) text {
	out := text{literal(open), false}
	for i, p := range parts {
		if i > 0 && sep != "" {
			out = join(out, text{literal(sep), false})
		}

		out = join(out, p)
	}

	return join(out, text{literal(close), false})
}

// concat writes the concatenation of the pieces as one string the
// caller owns. Two pieces are one tk_join, which allocates once; more
// are built in one allocation too: each text is held in a temporary,
// in order, so that the whole is sized from their lengths and the
// literals' before a byte is copied, then every piece is appended and
// the texts built here are dropped. fmt's printing goes through here,
// so a line of many operands is copied once, not once per operand.
func (e *Emitter) concat(pieces []piece) string {
	switch len(pieces) {
	case 0:
		return literal("")
	case 1:
		return built(pieces[0].asText())
	case 2:
		return built(join(pieces[0].asText(), pieces[1].asText()))
	}

	known := 0
	var lens []string
	for i, p := range pieces {
		if p.text.s == "" {
			known += len(p.lit)
			continue
		}

		tmp := e.temp()
		e.line("tk_string " + tmp + " = " + p.text.s + ";")
		pieces[i].text.s = tmp
		lens = append(lens, tmp+".len")
	}

	if len(lens) == 0 {
		all := make([]string, len(pieces))
		for i, p := range pieces {
			all[i] = p.lit
		}

		return built(text{literal(strings.Join(all, "")), false})
	}

	size := strings.Join(lens, " + ")
	if known > 0 {
		size += " + " + strconv.Itoa(known)
	}

	buf := e.temp()
	e.line("tk_slice_u8 " + buf + " = {0};")
	made := "tk_slice_make((tk_slice *)&" + buf + ", 0, " + size + ", 1);"
	if known == 0 {
		made = "if (" + size + " > 0) { " + made + " }" // an empty result owns nothing, so no block is made for it
	}

	e.line(made)
	for _, p := range pieces {
		e.line("tk_strings_appendString(&" + buf + ", " + p.asText().s + ");")
	}

	for _, p := range pieces {
		if p.text.owned {
			e.line("tk_string_drop(&" + p.text.s + ");")
		}
	}

	return "(tk_string){" + buf + ".ptr, " + buf + ".len, " + buf + ".cap}"
}

// asText returns the piece as a text: a literal is lent.
func (p piece) asText() text {
	if p.text.s == "" {
		return text{literal(p.lit), false}
	}

	return p.text
}
