// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"
	"strings"
)

// ObjectString returns the declaration of obj as the checker types it,
// for a reader: a function's or a method's signature with its
// parameters' names, func Largest<A: Shape, B: Shape>(a ref A, b ref B)
// f64; a struct, an enum or a trait with its type parameters, the
// traits it implements or refines and its members, one to a line; a
// variable, a field, a constant with its value, a variant with its
// fields, a type parameter with its bounds, a package's name with its
// path, a loop's part with the type it is a part of. A function of a
// generic type's instance, or an instance of a generic function, is
// written as its declaration. of is the type a function of a type
// belongs to, Point of func Point.Parse, which the function does not
// keep; nil for any other object.
func ObjectString(obj Object, of *TypeName) string {
	b := &strings.Builder{}
	switch obj := obj.(type) {
	case *Func:
		writeFunc(b, obj.Origin(), of)
	case *TypeName:
		writeTypeName(b, obj)
	case *Var:
		if obj.field {
			b.WriteString("field ")
		} else {
			b.WriteString("var ")
		}

		write(b, obj.name, " ", obj.typ.String())
	case *Const:
		write(b, "const ", obj.name, " ", obj.typ.String())
		if obj.val.kind != ConstUnknown {
			write(b, " = ", obj.val.text())
		}
	case *Variant:
		if obj.parent != nil {
			write(b, obj.parent.obj.name, ".")
		}

		b.WriteString(obj.name)
		writeVariantFields(b, obj.fields)
	case *PkgName:
		write(b, "package ", obj.name, " (", strconv.Quote(obj.path), ")")
	case *Builtin:
		write(b, "builtin ", obj.name)
	case *Part:
		writePart(b, obj)
	}

	return b.String()
}

// writeFunc writes a function's signature: func, the receiver of a
// method or the type a function of a type belongs to, the name, the
// type parameters with their bounds, the parameters with their names,
// and the results.
func writeFunc(b *strings.Builder, fn *Func, of *TypeName) {
	sig, ok := fn.typ.(*Signature)
	b.WriteString("func ")
	switch {
	case ok && sig.recv != nil:
		write(b, "(", sig.recv.name, " ", sig.recv.typ.String(), ") ")
	case of != nil:
		b.WriteString(of.name)
		if n, ok := of.typ.(*Named); ok {
			writeTypeParams(b, n.tparams, false)
		}

		b.WriteString(".")
	}

	b.WriteString(fn.name)
	if !ok {
		return
	}

	writeTypeParams(b, sig.tparams, true)
	b.WriteString("(")
	for i, p := range sig.params {
		if i > 0 {
			b.WriteString(", ")
		}

		if p.name != "" {
			write(b, p.name, " ")
		}

		if sig.variadic && i == len(sig.params)-1 {
			b.WriteString("...")
			switch kind, elem := sig.VariadicParam(); kind {
			case VariadicOwned:
				b.WriteString(elem.String())
				continue
			case VariadicLent, VariadicAny:
				write(b, "ref ", elem.String())
				continue
			}
		}

		b.WriteString(p.typ.String())
	}

	b.WriteString(")")
	if t, ok := sig.results.(*Tuple); !ok || t.Len() > 0 {
		write(b, " ", sig.results.String())
	}
}

// writeTypeName writes a type's declaration: a struct's or an enum's
// with its members, a trait's with its fields and methods, a type
// parameter's with its bounds, and any other type as type, its name
// and the type it is.
func writeTypeName(b *strings.Builder, tn *TypeName) {
	switch t := tn.typ.(type) {
	case *Named:
		switch u := t.underlying.(type) {
		case *Struct:
			b.WriteString("struct ")
			writeNamedHead(b, t)
			b.WriteString(" {\n")
			for _, f := range u.fields {
				write(b, "\t", f.name, " ", f.typ.String(), "\n")
			}

			b.WriteString("}")
		case *Enum:
			b.WriteString("enum ")
			writeNamedHead(b, t)
			b.WriteString(" {\n")
			for _, v := range u.variants {
				write(b, "\t", v.name)
				writeVariantFields(b, v.fields)
				b.WriteString("\n")
			}

			b.WriteString("}")
		default:
			b.WriteString("type ")
			writeNamedHead(b, t)
			if u != nil {
				write(b, " ", u.String())
			}
		}
	case *Trait:
		write(b, "trait ", tn.name)
		writeTypeParams(b, t.tparams, true)
		writeTraits(b, ": ", " + ", t.refines)
		b.WriteString(" {\n")
		for _, f := range t.fields {
			write(b, "\tvar ", f.name, " ", f.typ.String(), "\n")
		}

		for _, m := range append(t.required[:len(t.required):len(t.required)], t.defaults...) {
			b.WriteString("\t")
			writeFunc(b, m, nil)
			b.WriteString("\n")
		}

		b.WriteString("}")
	case *TypeParam:
		b.WriteString(tn.name)
		writeTraits(b, ": ", " + ", t.bounds)
	default:
		write(b, "type ", tn.name)
	}
}

// writeNamedHead writes a named type's name, its type parameters with
// their bounds and the traits it implements.
func writeNamedHead(b *strings.Builder, n *Named) {
	b.WriteString(n.obj.name)
	writeTypeParams(b, n.tparams, true)
	writeTraits(b, " impl ", ", ", n.impls)
}

// writeTypeParams writes a declaration's type parameters in angle
// brackets, with their bounds when bounds is set; nothing for none.
func writeTypeParams(b *strings.Builder, tparams []*TypeParam, bounds bool) {
	if len(tparams) == 0 {
		return
	}

	b.WriteString("<")
	for i, tp := range tparams {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(tp.obj.name)
		if bounds {
			writeTraits(b, ": ", " + ", tp.bounds)
		}
	}

	b.WriteString(">")
}

// writeTraits writes the traits after lead, each after the one before
// and sep, leaving out one that failed to resolve; nothing for none.
func writeTraits(b *strings.Builder, lead, sep string, traits []*Trait) {
	first := true
	for _, t := range traits {
		if t == nil {
			continue
		}

		if first {
			b.WriteString(lead)
			first = false
		} else {
			b.WriteString(sep)
		}

		b.WriteString(t.String())
	}
}

// writePart writes a loop's part: field or variant, its name, and what
// it is a part of, a variant's field the variant and its type.
func writePart(b *strings.Builder, p *Part) {
	kind := "field "
	if p.variant {
		kind = "variant "
	}

	write(b, kind, p.name, " of ")
	if p.in != nil {
		write(b, "variant ", p.in.name, " of ")
	}

	b.WriteString(p.of.String())
}

// write writes each of parts in turn.
func write(b *strings.Builder, parts ...string) {
	for _, p := range parts {
		b.WriteString(p)
	}
}

// writeVariantFields writes a variant's fields' types in parentheses;
// nothing for a variant with none.
func writeVariantFields(b *strings.Builder, fields []*Var) {
	if len(fields) == 0 {
		return
	}

	b.WriteString("(")
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(f.typ.String())
	}

	b.WriteString(")")
}
