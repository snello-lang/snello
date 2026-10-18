// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
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
		c.structLit(e, u)
	case *Slice:
		c.elements(e.Elts, u.elem)
	case *Array:
		c.elements(e.Elts, u.elem)
	case *Map:
		for _, elt := range e.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				c.errorf(elt.Pos(), "a map literal needs key: value elements")
				c.expr(elt, nil)
				continue
			}

			c.assign(c.expr(kv.Key, u.key), u.key, kv.Key.Pos(), "map key")
			c.assign(c.expr(kv.Value, u.value), u.value, kv.Value.Pos(), "map value")
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

func (c *Checker) structLit(e *ast.CompositeLit, st *Struct) {
	for _, elt := range e.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			c.errorf(elt.Pos(), "a struct literal names its fields: Name{field: value}")
			c.expr(elt, nil)
			continue
		}

		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			c.errorf(kv.Key.Pos(), "a struct literal key is a field name")
			c.expr(kv.Value, nil)
			continue
		}

		f := fieldOf(st, key.Name)
		if f == nil {
			c.errorf(key.Pos(), "unknown field "+key.Name)
			c.expr(kv.Value, nil)
			continue
		}

		c.recordUse(key, f)
		c.assign(c.expr(kv.Value, f.typ), f.typ, kv.Value.Pos(), "field "+f.name)
	}
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

		c.assign(c.expr(elt, elem), elem, elt.Pos(), "element")
	}
}
