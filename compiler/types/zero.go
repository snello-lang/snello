// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
)

// The refusals of zero values. No value is ever null or empty: a type
// without a zero value, hasZero's, is never given one, so each way the
// language has of making a zero refuses such a type, with what to write
// instead. A refusal only makes the checker accept fewer programs, so
// each keeps every guarantee the borrow rules give.

// zeroDecl refuses var x T with no value when T has no zero value.
func (c *Checker) zeroDecl(d *ast.VarDecl, t Type) {
	if t == Typ[Invalid] || hasZero(t) {
		return
	}

	c.errorf(d.Names[0].Pos(), "cannot declare "+d.Names[0].Name+" without a value: "+t.String()+" has no zero value; give it one, or declare it "+optionOf(t))
}

// zeroFields refuses a struct literal that leaves out a field whose
// type has no zero value, and one that leaves out a field the code
// cannot name, a field of another package's type that is not exported,
// when the type itself has none: the literal would build what only the
// type's package may, an empty handle among them.
func (c *Checker) zeroFields(e *ast.CompositeLit, t Type, st *Struct) {
	given := map[string]bool{}
	for _, elt := range e.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok {
				given[key.Name] = true
			}
		}
	}

	for _, f := range st.fields {
		if given[f.name] {
			continue
		}

		if !hasZero(f.typ) {
			c.errorf(e.Lbrace, "missing field "+f.name+" in a "+t.String()+" literal: "+f.typ.String()+" has no zero value; name it")
			continue
		}

		if owner := fieldOwner(t, f); owner != nil && owner != c.pkg && !ast.IsExported(f.name) && !hasZero(t) {
			c.errorf(e.Lbrace, "cannot build a "+t.String()+" with a literal: it has no zero value, and its field "+f.name+" is not exported; use its package's functions")
			return
		}
	}
}

// fieldOwner returns the package that declared field f of a value of
// type t: the field's own, else t's.
func fieldOwner(t Type, f *Var) *Package {
	if f.pkg != nil {
		return f.pkg
	}

	if n, ok := t.(*Named); ok {
		return n.obj.pkg
	}

	return nil
}

// zeroConversion refuses converting between two types when either has
// no zero value: a value of such a type is built only by its own
// package, and a type declared over its struct would otherwise build
// one with any fields it chose, sync.Counter(MyCounter{h: 5}), or take
// one apart and lose its Drop.
func (c *Checker) zeroConversion(e *ast.CallExpr, from, to Type) {
	if Identical(from, to) {
		return
	}

	for _, t := range []Type{to, from} {
		if !hasZero(t) {
			c.errorf(e.Args[0].Pos(), "cannot convert "+from.String()+" to "+to.String()+": "+t.String()+" has no zero value, and a value of it is built and taken apart only by its own package")
			return
		}
	}
}

// zeroArray refuses an array literal shorter than its length when the
// element has no zero value for the rest.
func (c *Checker) zeroArray(e *ast.CompositeLit, t Type, a *Array) {
	if int64(len(e.Elts)) < a.len && !hasZero(a.elem) {
		c.errorf(e.Lbrace, "too few elements for "+t.String()+": "+strconv.Itoa(len(e.Elts))+" of "+strconv.FormatInt(a.len, 10)+", and "+a.elem.String()+" has no zero value for the rest")
	}
}

// zeroMake refuses make([]T, n) when T has no zero value for the n
// elements, unless n is the constant 0.
func (c *Checker) zeroMake(e *ast.CallExpr, t Type) {
	s, ok := t.Underlying().(*Slice)
	if !ok || len(e.Args) < 2 || hasZero(s.elem) {
		return
	}

	if lit, ok := unparen(e.Args[1]).(*ast.BasicLit); ok && lit.Value == "0" {
		return
	}

	c.errorf(e.Args[1].Pos(), "make cannot fill a "+t.String()+": "+s.elem.String()+" has no zero value; use make("+t.String()+", 0, n) and Push")
}

// zeroRead refuses reading m[k] when the map's values have no zero
// value for a key the map does not hold.
func (c *Checker) zeroRead(e *ast.IndexExpr, m *Map) {
	if !hasZero(m.value) {
		c.errorf(e.Pos(), "cannot read "+exprText(e)+": "+m.value.String()+" has no zero value for a key the map does not hold; use "+exprText(e.X)+".Get("+exprText(e.Index)+")")
	}
}

// zeroWrite refuses writing part of m[k], m[k].f = v or m[k][i] = v,
// when the map's values have no zero value: a key the map does not hold
// gets an entry made from the zero value, and the write fills only the
// part.
func (c *Checker) zeroWrite(e *ast.IndexExpr, m *Map) {
	if !hasZero(m.value) {
		c.errorf(e.Pos(), "cannot write through "+exprText(e)+": "+m.value.String()+" has no zero value for a key the map does not hold; use "+exprText(e.X)+".GetMut("+exprText(e.Index)+")")
	}
}

// optionOf writes T?, the type of a value of T that may be missing,
// with the parentheses a container or a function type needs, since a ?
// after one applies to its element or its result; after a pointer it
// applies to the pointer.
func optionOf(t Type) string {
	switch t.(type) {
	case *Slice, *Map, *Chan, *Signature:
		return "(" + t.String() + ")?"
	}

	return t.String() + "?"
}
