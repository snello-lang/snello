// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"cmp"
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// initializers checks the top-level initializers of a file. They go
// before every body, so that a function may use a global whose type is
// inferred from its value.
func (c *Checker) initializers(f *ast.File) {
	for _, d := range f.Decls {
		if d, ok := d.(*ast.VarDecl); ok && !d.Const { // a constant was checked with the types (checkConsts)
			c.initializersOf(d, c.pkg.scope)
		}
	}
}

// funcBodies checks the body of every function and method of a file,
// after the initializers of every file, so that a function may use a
// global of another file whose type is inferred from its value.
func (c *Checker) funcBodies(f *ast.File) {
	for i, d := range f.Decls {
		if d, ok := d.(*ast.FuncDecl); ok && !c.badMethods[d] {
			outer := c.scope
			if scope := c.typeScopes[d]; scope != nil {
				c.scope = scope // the type parameters the receiver names
			}

			fn := c.objs[i].(*Func)
			c.word = c.words[fn]
			c.checkBody(fn.typ.(*Signature), d.Recv, d.Params, d.Body)
			c.word = nil
			c.scope = outer
			if d.Body != nil && (genericDecl(fn) || d.Of != nil && len(d.Of.Args) > 0) { // a function of a generic type is generic over the type's parameters
				fn.decl = d
			}
		}
	}
}

// checkBody checks one function's or method's body against its
// signature: the typing, then, when the body typed, the move and the
// borrow pass over its form. A closure's body is lowered with its
// function's and checked after it, on a form and a graph of its own.
func (c *Checker) checkBody(sig *Signature, recv *ast.Param, params []*ast.Param, body *ast.BlockStmt) {
	c.sig, c.recv = sig, sig.recv
	c.enclosing = c.enclosing[:0]
	first := len(c.errors)
	c.funcBody(sig, recv, params, body)
	if body != nil && len(c.errors) == first { // a body that typed: what the passes would say of one that did not follows from the typing's refusal
		form := c.lowerBody(sig, body) // the closures' forms, lowered inside their body's, are kept with it
		c.formMoves(form)              // each pass runs the closures' after their body's
		c.formBorrows(form, sig, token.NoPos)
		c.spareClosures(form)
	}

	c.sig, c.recv = nil, nil
	if c.sawParts {
		c.selectParts(body)
	}

	c.sawParts = false
}

// spareClosures keeps the forms of the closures written in the body
// whose form is f, checked now, for the closures lowered next, when no
// caller keeps the forms.
func (c *Checker) spareClosures(f *Form) {
	if c.info.Forms != nil {
		return
	}

	for _, cf := range f.closures {
		c.spareClosures(cf.form)
		c.spareForms = append(c.spareForms, cf.form)
	}
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

// initializersOf checks the values of a var declaration whose names
// are already declared in scope, giving an untyped name the type of its
// value.
func (c *Checker) initializersOf(d *ast.VarDecl, scope *Scope) {
	if len(d.Values) == 0 { // the parser requires a type when there is no value, resolved already
		if scope == c.pkg.scope {
			for _, name := range d.Names {
				if v, ok := scope.Lookup(name.Name).(*Var); ok {
					c.globalType(name, v.typ)
				}
			}
		}

		if v, ok := scope.Lookup(d.Names[0].Name).(*Var); ok {
			c.zeroDecl(d, v.typ)
		}

		return
	}

	var declared Type
	if d.Type != nil {
		declared = c.typ(d.Type)
	}

	first := len(c.errors)
	ops := c.values(len(d.Names), d.Values, declared, d.Pos(), nil)
	if scope == c.pkg.scope && len(c.errors) == first { // a package variable's values, typed: what they move and borrow the passes decide on their form
		form := c.lowerInit(d, declared)
		c.formMoves(form)
		c.formBorrows(form, nil, token.NoPos)
		c.spareClosures(form)
	}

	for i, name := range d.Names {
		c.assign(ops[i], declared, d.Values[min(i, len(d.Values)-1)].Pos(), "declaration")
		t := declared
		if declared == nil {
			t = inferred(ops[i])
			obj := scope.Lookup(name.Name)
			c.setType(obj, t)
			if v, ok := obj.(*Var); ok {
				delete(c.uninferred, v)
			}
		}

		if scope == c.pkg.scope { // every type is complete in this phase, declared or inferred
			c.globalType(name, t)
		}
	}
}

// values checks the right side of a declaration or assignment and
// returns one operand per name on the left: one value each, or the
// elements of a single tuple value. A count that does not match is
// reported and yields invalid operands.
//
// Several values are checked as the parts of one expression are, left
// to right, as a call's arguments are: what a value lends stays lent,
// and what it moves is moved, before the next is checked, so a later
// value can neither take nor change what an earlier one borrows, nor
// read what it moved. The statement is one full expression: what its
// values lend stays lent past every store, to where the statement ends,
// so a place a value borrows cannot be given another value. into gives
// the type of the place value i goes to, or nil when the value's own
// type is the place's: it is the value's hint, and decides what giving
// the value does (given); the move is recorded there, once, and the
// statement's own move of the value later finds it done.
func (c *Checker) values(n int, values []ast.Expr, hint Type, pos token.Pos, into func(i int) Type) []operand {
	ops := make([]operand, n)
	for i := range ops {
		ops[i] = invalidOperand
	}

	if len(values) == 1 && n > 1 {
		op := c.expr(values[0], nil)
		if op.mode == invalid {
			return ops
		}

		t, ok := valueType(op).Underlying().(*Tuple) // a declared tuple type is taken apart as the tuple it stands for
		if !ok || len(t.elems) != n {
			c.errorf(pos, "assignment mismatch: "+strconv.Itoa(n)+" names but a value of type "+op.typ.String())
			return ops
		}

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

	if n == 1 {
		ops[0] = c.expr(values[0], hint)
		return ops
	}

	for i, v := range values {
		t := hint
		if into != nil {
			t = cmp.Or(into(i), hint)
		}

		ops[i] = c.expr(v, t)
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

// hasMutParam reports whether a function has a receiver or parameter
// that is, or holds, a mut borrow: one through which it could store into
// the caller's places.
func (c *Checker) hasMutParam(sig *Signature) bool {
	if sig.recv != nil {
		if mut, ok := borrowIn(sig.recv.typ); ok && mut {
			return true
		}
	}

	for _, p := range sig.params {
		if mut, ok := borrowIn(p.typ); ok && mut {
			return true
		}
	}

	return false
}
