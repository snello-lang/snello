// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"strconv"
)

// literal types a literal by the type its context asks for: an integer
// literal is whatever numeric type is expected, else int; a float is
// f64 unless a float type is expected; a character is any integer type
// expected, else rune. There is no untyped constant arithmetic.
func (c *Checker) literal(lit *ast.BasicLit, expected Type) Type {
	switch lit.Kind {
	case token.INT:
		if t := numericExpected(expected, IsNumeric); t != nil {
			return t
		}

		return Typ[Int]
	case token.FLOAT:
		if t := numericExpected(expected, IsFloat); t != nil {
			return t
		}

		return Typ[Float64]
	case token.CHAR:
		if t := numericExpected(expected, IsInteger); t != nil {
			return t
		}

		return aliases[1] // rune
	case token.STRING:
		return Typ[String]
	}

	return Typ[Invalid]
}

// numericExpected returns expected when its underlying type is a basic
// type with one of the given properties, so a literal takes a declared
// type such as NodeID as readily as int.
func numericExpected(expected Type, info BasicInfo) Type {
	if expected == nil {
		return nil
	}

	if b, ok := expected.Underlying().(*Basic); ok && b.info&info != 0 {
		return expected
	}

	return nil
}

func (c *Checker) tuple(e *ast.TupleExpr, expected Type) operand {
	if len(e.Elts) == 0 {
		return operand{value, Unit, nil, nil}
	}

	var want *Tuple
	if expected != nil {
		want, _ = expected.Underlying().(*Tuple)
	}

	elems := make([]Type, len(e.Elts))
	for i, elt := range e.Elts {
		var hint Type
		if want != nil && i < len(want.elems) {
			hint = want.elems[i]
		}

		op := c.expr(elt, hint)
		elems[i] = valueType(op)
		c.maybeMove(op, elems[i]) // the part moves into the tuple
	}

	return operand{value, NewTuple(elems...), nil, nil}
}

// compositeLit checks T{...}. A struct literal names its fields; a
// slice, array or map literal lists its elements, and an element that
// is itself a literal may leave its type out.
func (c *Checker) compositeLit(e *ast.CompositeLit, expected Type) operand {
	if g := c.bareGeneric(e.Type); g != nil {
		return c.genericLit(e, g, expected)
	}

	t := expected
	if e.Type != nil {
		t = c.typ(e.Type)
	}

	if t == nil {
		c.errorf(e.Lbrace, "a composite literal needs a type here")
		c.looseElements(e.Elts)
		return invalidOperand
	}

	switch u := t.Underlying().(type) {
	case *Struct:
		c.structLit(e, t, u, nil)
	case *Slice:
		c.elements(e.Elts, u.elem)
	case *Array:
		if int64(len(e.Elts)) > u.len {
			c.errorf(e.Lbrace, "too many elements for "+t.String()+": "+strconv.Itoa(len(e.Elts)))
		}

		c.elements(e.Elts, u.elem) // fewer than the length leaves the rest zero, as in Go
	case *Map:
		for _, elt := range e.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				c.errorf(elt.Pos(), "a map literal needs key: value elements")
				c.expr(elt, nil)
				continue
			}

			c.assign(c.expr(kv.Key, u.key), u.key, kv.Key.Pos(), "map key")
			v := c.expr(kv.Value, u.value)
			c.assign(v, u.value, kv.Value.Pos(), "map value")
			c.kept(v, "stored", kv.Value.Pos())
		}
	default:
		if t != Typ[Invalid] {
			c.errorf(e.Lbrace, "cannot build a value of type "+t.String()+" with a composite literal")
		}

		c.looseElements(e.Elts)
		return invalidOperand
	}

	return operand{value, t, nil, nil}
}

// bareGeneric returns the generic struct a composite literal names
// without type arguments, Pair{a: 1, b: 2.0}, whose arguments are
// inferred; or nil.
func (c *Checker) bareGeneric(t ast.Type) *Named {
	n, ok := t.(*ast.NamedType)
	if !ok || n.Pkg != nil || len(n.Args) > 0 {
		return nil
	}

	_, obj := c.scope.LookupParent(n.Name.Name)
	tn, ok := obj.(*TypeName)
	if !ok {
		return nil
	}

	g, ok := tn.typ.(*Named)
	if !ok || g.tparams == nil {
		return nil
	}

	if _, isStruct := g.underlying.(*Struct); !isStruct {
		return nil // namedType reports the missing arguments
	}

	c.recordUse(n.Name, tn)
	return g
}

// genericLit checks a literal of a generic struct written without type
// arguments: they come from the type the context expects, then from
// the field values.
func (c *Checker) genericLit(e *ast.CompositeLit, g *Named, expected Type) operand {
	in := newInference(g.tparams)
	in.unify(g, expected)

	st := g.underlying.(*Struct)
	if !c.keyed(e, g, st) {
		return invalidOperand
	}

	var values []ast.Expr
	var slots []Type
	for _, elt := range e.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok {
				if f := fieldNamed(st.fields, key.Name); f != nil {
					values = append(values, kv.Value)
					slots = append(slots, f.typ)
				}
			}
		}
	}

	ops := c.evaluate(in, values, slots, func(i int, op operand) { c.maybeMove(op, slots[i]) })
	targs, ok := c.complete(in, g.obj.name, e.Type, nil)
	if !ok {
		return invalidOperand
	}

	t := Instantiate(g, targs)
	done := make(map[ast.Expr]operand, len(values))
	for i, v := range values {
		done[v] = ops[i]
	}

	c.structLit(e, t, t.Underlying().(*Struct), done)
	return operand{value, t, nil, nil}
}

// structLit checks the fields of a struct literal. done holds the
// values already evaluated, and moved, while the struct's type
// arguments were inferred; they are only checked against their field.
func (c *Checker) structLit(e *ast.CompositeLit, t Type, st *Struct, done map[ast.Expr]operand) {
	if !c.keyed(e, t, st) {
		return
	}

	for _, elt := range e.Elts {
		kv := elt.(*ast.KeyValueExpr)

		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			c.errorf(kv.Key.Pos(), "a struct literal key is a field name")
			c.expr(kv.Value, nil)
			continue
		}

		f := fieldNamed(st.fields, key.Name)
		if f == nil {
			c.errorf(key.Pos(), "unknown field "+key.Name)
			c.expr(kv.Value, nil)
			continue
		}

		if !c.visible(t, key) {
			c.expr(kv.Value, nil)
			continue
		}

		c.recordUse(key, f)
		v, evaluated := done[kv.Value]
		if evaluated {
			c.fits(v, f.typ, kv.Value.Pos(), "field "+f.name)
		} else {
			v = c.expr(kv.Value, f.typ)
			c.assign(v, f.typ, kv.Value.Pos(), "field "+f.name)
		}

		c.kept(v, "stored", kv.Value.Pos())
	}
}

// keyed makes every element of a struct literal a key: value pair, and
// reports whether it could. A literal names its fields, Vec3{x: 1, y:
// 2, z: 3}, or gives every field in order, Vec3{1, 2, 3}, as Go
// allows; the positional form is rewritten to the named one, each
// value under the name of its field, so that everything after the
// check sees one shape. Mixing the two, or giving the wrong number of
// values, is reported.
func (c *Checker) keyed(e *ast.CompositeLit, t Type, st *Struct) bool {
	positional := 0
	for _, elt := range e.Elts {
		if _, ok := elt.(*ast.KeyValueExpr); !ok {
			positional++
		}
	}

	if positional == 0 {
		return true
	}

	if positional < len(e.Elts) {
		c.errorf(e.Lbrace, "a struct literal names its fields or gives them all in order, not both")
		c.looseElements(e.Elts)
		return false
	}

	if positional != len(st.fields) {
		c.errorf(e.Lbrace, "a positional struct literal gives every field: "+t.String()+" has "+plural(len(st.fields), "field")+", not "+strconv.Itoa(positional))
		c.looseElements(e.Elts)
		return false
	}

	for i, elt := range e.Elts {
		e.Elts[i] = &ast.KeyValueExpr{Key: &ast.Ident{NamePos: elt.Pos(), Name: st.fields[i].name}, Value: elt}
	}

	return true
}

// looseElements checks the elements of a literal whose type is unknown,
// so their own errors are reported; a bare key is a field name and is
// left alone.
func (c *Checker) looseElements(elts []ast.Expr) {
	for _, elt := range elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if _, isName := kv.Key.(*ast.Ident); !isName {
				c.expr(kv.Key, nil)
			}

			c.expr(kv.Value, nil)
			continue
		}

		c.expr(elt, nil)
	}
}

func (c *Checker) elements(elts []ast.Expr, elem Type) {
	for _, elt := range elts {
		if _, ok := elt.(*ast.KeyValueExpr); ok {
			c.errorf(elt.Pos(), "a slice literal lists its elements without keys")
			continue
		}

		v := c.expr(elt, elem)
		c.assign(v, elem, elt.Pos(), "element")
		c.kept(v, "stored", elt.Pos())
	}
}
