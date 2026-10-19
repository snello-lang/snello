// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// resolveBodies checks every top-level initializer and function body,
// and the bodies of the traits' default methods. Initializers go first
// so that a function may use a global whose type is inferred from its
// value.
func (c *Checker) resolveBodies(f *ast.File) {
	for _, d := range f.Decls {
		if d, ok := d.(*ast.VarDecl); ok {
			c.initializers(d, c.pkg.scope)
		}
	}

	for i, d := range f.Decls {
		if d, ok := d.(*ast.FuncDecl); ok && !c.badMethods[d] {
			outer := c.scope
			if scope := c.typeScopes[d]; scope != nil {
				c.scope = scope // the type parameters the receiver names
			}

			c.checkBody(c.objs[i].(*Func).typ.(*Signature), d.Recv, d.Params, d.Body)
			c.scope = outer
		}
	}

	c.checkDefaultBodies()
}

// checkBody checks one function's or method's body against its
// signature. Each body starts afresh, with no moved places, and its
// borrows are resolved at its end; a closure's body shares its
// function's state.
func (c *Checker) checkBody(sig *Signature, recv *ast.Param, params []*ast.Param, body *ast.BlockStmt) {
	c.sig, c.recv = sig, sig.recv
	c.moved, c.floors, c.dead = c.moved[:0], c.floors[:0], false
	c.funcBody(sig, recv, params, body)
	c.resolveBorrows()
	c.sig, c.recv = nil, nil
}

// funcBody opens the function scope, declares the receiver and the
// parameters under their names with the variables the signature already
// holds, checks the statements, and requires a function with results
// to end in a statement that leaves.
func (c *Checker) funcBody(sig *Signature, recv *ast.Param, params []*ast.Param, body *ast.BlockStmt) {
	if body == nil {
		return
	}

	scope := c.openScope()
	if recv != nil {
		c.declare(scope, recv.Name, sig.recv)
	}

	for i, p := range params {
		if p.Name != nil {
			c.declare(scope, p.Name, sig.params[i])
		}
	}

	if !c.stmts(body.List) && sig.results != Unit {
		c.errorf(body.Rbrace, "missing return at the end of the function")
	}

	c.closeScope()
}

// initializers checks the values of a var or const declaration whose
// names are already declared in scope, giving an untyped name the type
// of its value.
func (c *Checker) initializers(d *ast.VarDecl, scope *Scope) {
	if len(d.Values) == 0 {
		return // the parser requires a type when there is no value
	}

	var declared Type
	if d.Type != nil {
		declared = c.typ(d.Type)
	}

	ops := c.values(len(d.Names), d.Values, declared, d.Pos())
	for i, name := range d.Names {
		c.assign(ops[i], declared, d.Values[min(i, len(d.Values)-1)].Pos(), "declaration")
		if declared == nil {
			c.setType(scope.Lookup(name.Name), inferred(ops[i]))
		}
	}
}

// values checks the right side of a declaration or assignment and
// returns one operand per name on the left: one value each, or the
// elements of a single tuple value. A count that does not match is
// reported and yields invalid operands.
func (c *Checker) values(n int, values []ast.Expr, hint Type, pos token.Pos) []operand {
	ops := make([]operand, n)
	for i := range ops {
		ops[i] = invalidOperand
	}

	if len(values) == 1 && n > 1 {
		op := c.expr(values[0], nil)
		if op.mode == invalid {
			return ops
		}

		t, ok := valueType(op).(*Tuple)
		if !ok || len(t.elems) != n {
			c.errorf(pos, "assignment mismatch: "+strconv.Itoa(n)+" names but a value of type "+op.typ.String())
			return ops
		}

		c.maybeMove(op, valueType(op)) // the parts move out of the tuple
		for i := range ops {
			ops[i] = operand{value, t.elems[i], nil, nil}
		}

		return ops
	}

	if len(values) != n {
		c.errorf(pos, "assignment mismatch: "+strconv.Itoa(n)+" names but "+strconv.Itoa(len(values))+" values")
		c.exprs(values)
		return ops
	}

	for i, v := range values {
		ops[i] = c.expr(v, hint)
	}

	return ops
}

// setType gives a variable or constant declared without a type the
// type its value has.
func (c *Checker) setType(obj Object, t Type) {
	switch obj := obj.(type) {
	case *Var:
		if obj.typ == nil {
			obj.typ = t
		}
	case *Const:
		if obj.typ == nil {
			obj.typ = t
		}
	}
}

// inferred is the type a declaration x := e gives x: the value's type,
// with a borrow kept as a borrow, so n := ref t.nodes[i] makes n a
// ref Node and x := n.key makes x an int.
func inferred(op operand) Type {
	if op.mode == invalid {
		return Typ[Invalid]
	}

	if isNever(op.typ) {
		return Typ[Invalid]
	}

	return op.typ
}
