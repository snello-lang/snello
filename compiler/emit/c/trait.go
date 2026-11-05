// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A trait used as a type is an interface value: a pointer to a table
// and a pointer to the data it owns, on the heap. The table type is
// written once per trait, with one function per method a value of the
// trait can call, one accessor per trait field, and the drop. One
// table is written per implementing type and trait, when a value of
// the type first becomes a value of the trait: each entry is a small
// adapter that casts the data pointer back to the type and calls the
// method the type has for that name, resolved as any call on the type
// would be, so an inherited default or a generic instance's method
// serves as well as an own method. Making the value is one allocation
// and a copy; calling a method is one indirect call. A ref receiver is
// passed as the value in C, so the adapter copies the data for it; a
// mut receiver is the pointer; a value receiver takes the data and
// frees the box, as the trait value was consumed.

// defineTrait writes the two C structs of a trait used as a type: the
// value, then the table, whose entries may take the value itself.
func (e *Emitter) defineTrait(t *types.Trait, pos token.Pos) {
	name := e.typeName(t, pos)
	if e.state[name] == defined {
		return
	}

	e.state[name] = defined
	put(&e.types, "struct ", name, " {\n\tconst ", tableType(t), " *tab;\n\tvoid *data;\n};\n\n")
	var b strings.Builder
	b.WriteString("\tvoid (*drop)(void *);\n")
	for _, fn := range t.ValueMethods() {
		put(&b, "\t", e.entry(fn, pos), ";\n")
	}

	for _, f := range t.ValueFields() {
		put(&b, "\t", e.typeName(f.Type(), pos), " *(*", fieldAccessor(f), ")(void *);\n")
	}

	put(&e.types, "struct ", tableType(t), " {\n", b.String(), "};\n\n")
}

// entry writes the table entry for the method fn: a pointer to a
// function taking the data and the method's parameters. The types in
// it are declared, not defined, which is all a declarator needs in C
// and lets a struct holding a trait value appear in the trait's methods
// while the struct is still being defined.
func (e *Emitter) entry(fn *types.Func, pos token.Pos) string {
	sig := fn.Type().(*types.Signature)
	result := "void"
	if sig.Results() != types.Unit {
		result = e.typeName(sig.Results(), pos)
	}

	var b strings.Builder
	put(&b, result, " (*", lower.Ident(fn.Name()), ")(void *")
	for _, p := range sig.Params() {
		put(&b, ", ", e.typeName(p.Type(), pos))
	}

	b.WriteByte(')')
	return b.String()
}

// tableType is the C name of a trait's table type.
func tableType(t *types.Trait) string {
	return "sn_" + lower.Ident(t.Obj().Name()) + "_tab"
}

// fieldAccessor is the table entry reaching a trait field, apart from
// the methods beside it.
func fieldAccessor(f *types.Var) string {
	return "f_" + lower.Ident(f.Name())
}

// box writes the conversion of v, a C value of the declared type from,
// into a value of the trait type to: the function doing it is written
// once per pair, with the table it points at.
func (e *Emitter) box(v string, from *types.Named, to *types.Trait, pos token.Pos) string {
	name := "sn_" + lower.NamedName(from) + "_as_" + lower.Ident(to.Obj().Name())
	if e.state[name] == undeclared {
		e.state[name] = defined
		e.table(name, from, to, pos)
	}

	return name + "(" + v + ")"
}

// table writes the adapters, the table and the boxing function for the
// declared type from used as the trait to, under the given name.
func (e *Emitter) table(name string, from *types.Named, to *types.Trait, pos token.Pos) {
	box := e.cType(to, pos) // the table type, before a table of it
	typ := e.cType(from, pos)
	var entries []string
	var b strings.Builder
	drop := "(void)0;"
	if needsDrop(from) {
		drop = e.dropper(from, pos) + "(p);"
	}

	put(&b, "static void ", name, "_drop(void *sn_p) {\n\t", typ, " *p = sn_p;\n\t", drop, "\n\tsn_free(p);\n}\n\n")
	entries = append(entries, name+"_drop")
	for _, fn := range to.ValueMethods() {
		adapter := name + "_" + lower.Ident(fn.Name())
		entries = append(entries, adapter)
		e.adapter(&b, adapter, typ, from, fn, pos)
	}

	for _, f := range to.ValueFields() {
		accessor := name + "_" + fieldAccessor(f)
		entries = append(entries, accessor)
		put(&b, "static ", e.cType(f.Type(), pos), " *", accessor, "(void *sn_p) {\n\treturn &((", typ, " *)sn_p)->", lower.Ident(f.Name()), ";\n}\n\n")
	}

	put(&b, "static const ", tableType(to), " ", name, "_tab = {", strings.Join(entries, ", "), "};\n\n")
	put(&b, "static SN_UNUSED ", box, " ", name, "(", typ, " v) {\n\t", typ, " *p = sn_alloc(sizeof(", typ, "));\n\t*p = v;\n\treturn (", box, "){&", name, "_tab, p};\n}\n\n")
	put(&e.protos, "static SN_UNUSED ", box, " ", name, "(", typ, " v);\n")
	e.helpers.WriteString(b.String())
}

// adapter writes the table entry for one method: it takes the data
// pointer and the method's parameters, and calls the method the type
// from has under that name, with the receiver as the method wants it.
func (e *Emitter) adapter(b *strings.Builder, name, typ string, from *types.Named, fn *types.Func, pos token.Pos) {
	sig := fn.Type().(*types.Signature)
	target, tsig := e.methodNameOf(nil, from, fn.Name(), pos)
	if tsig == nil {
		return
	}

	recv := "*p"
	free := ""
	switch r := tsig.Recv().Type().(type) { // the receiver is Self, ref Self or mut Self, as the checker insists
	case *types.Ref:
		if r.Mut() {
			recv = "p"
		}
	default:
		free = "\tsn_free(p);\n" // a value receiver takes the data; the trait value was consumed
	}

	args := []string{recv}
	var params strings.Builder
	for i, p := range sig.Params() {
		arg := "a" + strconv.Itoa(i) // positional: a required method may leave its parameters unnamed
		args = append(args, arg)
		put(&params, ", ", e.cType(p.Type(), pos), " ", arg)
	}

	call := target + "(" + strings.Join(args, ", ") + ")"
	put(b, "static ", e.resultType(sig, pos), " ", name, "(void *sn_p", params.String(), ") {\n\t", typ, " *p = sn_p;\n")
	switch {
	case sig.Results() == types.Unit:
		put(b, "\t", call, ";\n", free)
	case free == "":
		put(b, "\treturn ", call, ";\n")
	default:
		put(b, "\t", e.resultType(sig, pos), " r = ", call, ";\n", free, "\treturn r;\n")
	}

	b.WriteString("}\n\n")
}

// methodNameOf returns the C name of the method the declared type t
// has under name, and its signature: an own method, an inherited
// default or a generic instance's method, asked for as a call on a
// value of t would ask; id is the call's selector when there is one,
// for the type arguments of a generic method. A method the type does
// not have is reported, with a nil signature.
func (e *Emitter) methodNameOf(id *ast.Ident, t *types.Named, name string, pos token.Pos) (string, *types.Signature) {
	fn := t.Method(name)
	if fn == nil { // a library type's method, as Drop on os.File, until the library is written in Snello
		e.unsupported(pos, "the method "+name+" of a trait on "+t.String())
		return "0", nil
	}

	sig := fn.Type().(*types.Signature)
	if fn.Derived() { // written from the type's parts, as the functions the backend has for them
		switch name {
		case "Eq":
			return e.comparer(t, pos), sig
		case "Cmp":
			return e.orderer(t, pos), sig
		case "Hash":
			return e.hasher(t, pos), sig
		case "Clone":
			return e.cloner(t, pos), sig
		}
	}

	if e.generics[fn.Origin()] != nil {
		return e.callInstance(id, fn, sig.Recv().Type())
	}

	return lower.MethodName(sig.Recv().Type(), name), sig
}

// traitCall writes the call x of a method on the trait value sel.X,
// wanted as recvType: one indirect call through the table, with the
// data pointer first. The value is read twice, for its table and its
// data, so it is saved in a temporary even when it is a variable; a
// value built here and only lent is dropped when the statement ends,
// as any such operand is.
func (e *Emitter) traitCall(x *ast.CallExpr, sel *ast.SelectorExpr, recvType types.Type, sig *types.Signature) string {
	recv := e.operands([]ast.Expr{sel.X}, []types.Type{recvType})[0]
	v := e.temp()
	e.line(e.cType(recvType, x.Pos()) + " " + v + " = " + recv + ";")
	v = deref(v, recvType)
	call := v + ".tab->" + lower.Ident(sel.Sel.Name) + "(" + v + ".data"
	if len(x.Args) > 0 {
		call += ", " + e.args(nil, x.Args, sig)
	}

	return call + ")"
}

// field writes the field f of the value v of type base: a member of a
// struct, or, when base is a trait, a place in the box reached through
// the table's accessor, which can be read, written and emptied as any
// field.
func field(v string, base types.Type, f *types.Var) string {
	if _, ok := lower.ValueType(base).(*types.Trait); ok {
		return "(*" + v + ".tab->" + fieldAccessor(f) + "(" + v + ".data))"
	}

	return v + "." + lower.Ident(f.Name())
}

// traitDrop writes the body of the drop function of a trait type: the
// table's drop, unless the value moved away and left no table.
func traitDrop(b *strings.Builder) {
	b.WriteString("\tif (v->tab != NULL) {\n\t\tv->tab->drop(v->data);\n\t\tv->tab = NULL;\n\t}\n")
}
