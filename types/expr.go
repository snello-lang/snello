// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// expr checks e and records its type. expected is the type the context
// asks for, or nil: it gives a literal its type, None and Some their
// element, and an elided composite literal its type. It is a hint, not
// a check; the caller decides whether the result fits.
func (c *Checker) expr(e ast.Expr, expected Type) operand {
	c.depth++
	op := c.exprInternal(e, expected)
	if c.depth--; c.depth == 0 {
		c.temps = c.temps[:0] // what the full expression lent is given back
	}

	if op.typ == Typ[Invalid] {
		return invalidOperand // reported where the type went wrong; never again
	}

	if op.mode != invalid {
		c.recordType(e, op.typ)
		op.expr = e
	}

	return op
}

func (c *Checker) exprInternal(e ast.Expr, expected Type) operand {
	switch e := e.(type) {
	case *ast.Ident:
		return c.ident(e, expected)
	case *ast.BasicLit:
		return operand{value, c.literal(e, expected), nil, nil}
	case *ast.ParenExpr:
		return c.expr(e.X, expected)
	case *ast.TupleExpr:
		return c.tuple(e, expected)
	case *ast.SelectorExpr:
		return c.selector(e)
	case *ast.IndexExpr:
		return c.index(e)
	case *ast.SliceExpr:
		return c.sliceExpr(e)
	case *ast.CallExpr:
		return c.call(e, expected)
	case *ast.UnaryExpr:
		return c.unary(e, expected)
	case *ast.BinaryExpr:
		return c.binary(e, expected)
	case *ast.CoalesceExpr:
		return c.coalesce(e, expected)
	case *ast.TypeExpr:
		return operand{typeOperand, c.typ(e.Type), nil, nil}
	case *ast.TryExpr:
		return c.try(e)
	case *ast.CompositeLit:
		return c.compositeLit(e, expected)
	case *ast.KeyValueExpr:
		c.errorf(e.Pos(), "key: value is only allowed inside a composite literal")
	case *ast.FuncLit:
		return c.funcLit(e)
	case *ast.IfExpr:
		return c.ifExpr(e, expected)
	case *ast.MatchExpr:
		return c.matchExpr(e, expected)
	}

	return invalidOperand
}

// ident resolves a name and says what kind of thing it is.
func (c *Checker) ident(e *ast.Ident, expected Type) operand {
	if isBlank(e) {
		c.errorf(e.Pos(), "cannot use _ as a value")
		return invalidOperand
	}

	obj := c.lookup(e)
	if obj == nil {
		return invalidOperand
	}

	switch obj := obj.(type) {
	case *Var:
		if obj.typ == nil {
			return invalidOperand // declared with an error
		}

		if !c.noUse || c.inner {
			obj.lastUse = e.Pos() // a borrow lives to here; assigning the whole variable ends it instead
		}

		c.checkUse(e)
		return operand{variable, obj.typ, obj, nil}
	case *Const:
		if obj.typ == nil {
			return invalidOperand
		}

		return operand{value, obj.typ, obj, nil}
	case *Func:
		return operand{value, obj.typ, obj, nil}
	case *TypeName:
		return operand{typeOperand, obj.typ, obj, nil}
	case *Builtin:
		c.errorf(e.Pos(), obj.name+" must be called")
	case *Variant:
		return c.constantVariant(e, obj, expected)
	case *PkgName:
		c.errorf(e.Pos(), "use of package "+obj.name+" without a selector")
	}

	return invalidOperand
}

// constantVariant types a variant written without arguments: Closed,
// None. One with fields is a constructor and must be called.
func (c *Checker) constantVariant(e *ast.Ident, v *Variant, expected Type) operand {
	if v.parent == nil && v != NoneVariant {
		c.errorf(e.Pos(), v.name+" must be called: "+v.name+"(x)")
		return invalidOperand
	}

	if v == NoneVariant {
		if expected == Typ[Invalid] {
			return invalidOperand
		}

		if expected != nil {
			if _, ok := expected.Underlying().(*Option); ok {
				return operand{value, expected, v, nil}
			}
		}

		c.errorf(e.Pos(), "cannot tell the type of None here; the context gives it no type")
		return invalidOperand
	}

	if len(v.fields) > 0 {
		c.errorf(e.Pos(), v.name+" has fields and must be written "+v.name+"(...)")
		return invalidOperand
	}

	return operand{value, v.parent, v, nil}
}

// selector checks x.Sel: a package member, a field, a method of a
// declared type, or a predeclared method. Fields and methods are found
// through any borrow or owned pointer, so n.value works whether n is a
// Node, a ref Node, or a *Node.
func (c *Checker) selector(e *ast.SelectorExpr) operand {
	if ident, ok := e.X.(*ast.Ident); ok {
		if _, obj := c.scope.LookupParent(ident.Name); obj != nil {
			if pkg, ok := obj.(*PkgName); ok {
				c.recordUse(ident, pkg)
				return c.packageMember(e, pkg)
			}
		}
	}

	wasInner := c.inner
	c.inner = isPlaceChain(e.X) // the whole selector is the use, checked below
	x := c.expr(e.X, nil)
	c.inner = wasInner
	if x.mode == invalid {
		return invalidOperand
	}

	base, m := deref(x)
	if named, ok := base.(*Named); ok {
		if st, ok := named.underlying.(*Struct); ok {
			if f := fieldOf(st, e.Sel.Name); f != nil {
				c.recordUse(e.Sel, f)
				c.checkUse(e)
				return operand{m, f.typ, f, nil}
			}
		}

		if fn := named.Method(e.Sel.Name); fn != nil {
			c.recordUse(e.Sel, fn)
			c.receiver(e, x, fn.typ.(*Signature))
			return operand{value, fn.typ, fn, nil}
		}
	}

	if sig := builtinMethod(base, e.Sel.Name); sig != nil {
		c.receiver(e, x, sig)
		return operand{value, sig, nil, nil}
	}

	c.errorf(e.Sel.Pos(), e.Sel.Name+" is not a field or method of "+x.typ.String())
	return invalidOperand
}

// receiver checks the receiver of a method against what the method
// asks of it: a value receiver takes it, so an owned receiver moves and
// a borrow variable is lent again; a mut receiver needs a place that
// can be changed, which a value reached through a ref is not. The
// borrow the call creates is checked in call, after the arguments.
func (c *Checker) receiver(e *ast.SelectorExpr, x operand, sig *Signature) {
	c.checkUse(e.X)
	r, ok := sig.recv.typ.(*Ref)
	if !ok {
		if !isMut(x.typ) {
			c.maybeMove(x, sig.recv.typ)
		}

		return
	}

	if r.mut && x.mode == borrowed {
		c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut through a ref "+c.throughWhat(e.X))
	}
}

func (c *Checker) packageMember(e *ast.SelectorExpr, pkg *PkgName) operand {
	obj := libraryMember(pkg.path, e.Sel.Name)
	if obj == nil {
		c.errorf(e.Sel.Pos(), "undefined: "+pkg.name+"."+e.Sel.Name)
		return invalidOperand
	}

	c.recordUse(e.Sel, obj)
	switch obj := obj.(type) {
	case *Func:
		return operand{value, obj.typ, obj, nil}
	case *Var:
		return operand{variable, obj.typ, obj, nil}
	case *TypeName:
		return operand{typeOperand, obj.typ, obj, nil}
	}

	return invalidOperand
}

func (c *Checker) index(e *ast.IndexExpr) operand {
	wasInner := c.inner
	c.inner = isPlaceChain(e.X) // the whole index is the use, checked below
	x := c.expr(e.X, nil)
	c.inner = wasInner
	if x.mode == invalid {
		return invalidOperand
	}

	base, m := deref(x)
	switch t := base.Underlying().(type) {
	case *Slice:
		c.intIndex(e.Index)
		c.checkUse(e)
		return operand{m, t.elem, nil, nil}
	case *Array:
		c.intIndex(e.Index)
		c.checkUse(e)
		return operand{m, t.elem, nil, nil}
	case *Map:
		c.assign(c.operandOf(e.Index, t.key), t.key, e.Index.Pos(), "map key")
		c.checkUse(e)
		return operand{m, t.value, nil, nil}
	case *Basic:
		if t.kind == String {
			c.intIndex(e.Index)
			c.checkUse(e)
			return operand{value, aliases[0], nil, nil} // a byte, by that name
		}
	}

	c.errorf(e.Pos(), "cannot index a value of type "+x.typ.String())
	return invalidOperand
}

// intIndex checks an index expression, which may be of any integer
// type, including a declared one such as NodeID.
func (c *Checker) intIndex(e ast.Expr) {
	op := c.operandOf(e, Typ[Int])
	if op.mode != invalid && !hasInfo(valueType(op), IsInteger) {
		c.errorf(e.Pos(), "index must be an integer, not "+op.typ.String())
	}
}

// sliceExpr checks x[lo:hi], which lends the piece: a ref string or a
// ref []T, mut when x is reached through a mut borrow.
func (c *Checker) sliceExpr(e *ast.SliceExpr) operand {
	x := c.expr(e.X, nil)
	if x.mode == invalid {
		return invalidOperand
	}

	if e.Low != nil {
		c.intIndex(e.Low)
	}

	if e.High != nil {
		c.intIndex(e.High)
	}

	base, m := deref(x)
	switch t := base.Underlying().(type) {
	case *Slice:
		return operand{value, NewRef(t, m == mutBorrowed), nil, nil}
	case *Basic:
		if t.kind == String {
			return operand{value, NewRef(Typ[String], false), nil, nil}
		}
	}

	c.errorf(e.Pos(), "cannot slice a value of type "+x.typ.String())
	return invalidOperand
}

func (c *Checker) unary(e *ast.UnaryExpr, expected Type) operand {
	switch e.Op {
	case token.REF, token.MUT:
		x := c.expr(e.X, paramHint(expected))
		if x.mode == invalid {
			return invalidOperand
		}

		if e.Op == token.MUT && x.mode == borrowed {
			c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut through a ref "+c.throughWhat(e.X))
			return invalidOperand
		}

		t := x.typ
		if r, ok := t.(*Ref); ok {
			t = r.elem // a borrow of a borrow is a borrow of the value
		}

		c.lend(e.X, e.Op == token.MUT, e.X.Pos())
		return operand{value, NewRef(t, e.Op == token.MUT), nil, nil}
	case token.AND:
		var hint Type
		if p, ok := underlyingPointer(expected); ok {
			hint = p.elem
		}

		x := c.expr(e.X, hint)
		if x.mode == invalid {
			return invalidOperand
		}

		c.maybeMove(x, valueType(x)) // &x moves x to the heap
		return operand{value, NewPointer(valueType(x)), nil, nil}
	case token.MUL:
		x := c.operandOf(e.X, nil) // reaching through p reads p, even to write what it holds
		if x.mode == invalid {
			return invalidOperand
		}

		switch t := x.typ.(type) {
		case *Ref:
			m := borrowed
			if t.mut {
				m = mutBorrowed
			}

			return operand{m, t.elem, nil, nil}
		case *Pointer:
			return operand{variable, t.elem, nil, nil}
		}

		c.errorf(e.OpPos, "cannot dereference a value of type "+x.typ.String())
		return invalidOperand
	case token.ARROW:
		x := c.expr(e.X, nil)
		if x.mode == invalid {
			return invalidOperand
		}

		if ch, ok := valueType(x).Underlying().(*Chan); ok {
			return operand{value, NewOption(ch.elem), nil, nil}
		}

		c.errorf(e.OpPos, "cannot receive from a value of type "+x.typ.String())
		return invalidOperand
	}

	x := c.expr(e.X, expected)
	if x.mode == invalid {
		return invalidOperand
	}

	t := readType(x)
	var need BasicInfo
	switch e.Op {
	case token.SUB:
		need = IsNumeric
	case token.NOT:
		need = IsBoolean
	case token.XOR:
		need = IsInteger
	}

	if !hasInfo(t, need) {
		c.errorf(e.OpPos, "operator "+e.Op.String()+" is not defined on "+x.typ.String())
		return invalidOperand
	}

	return operand{value, t, nil, nil}
}

// binary checks x op y. The two sides must have the same type; a
// literal on one side takes the other side's type, so v*10 is f64 when
// v is. A value reached through a borrow of a Copy type is read as the
// value, so n.key < k works through a ref Node.
func (c *Checker) binary(e *ast.BinaryExpr, expected Type) operand {
	var x, y operand
	if isLiteral(e.X) && !isLiteral(e.Y) {
		y = c.expr(e.Y, expected)
		x = c.expr(e.X, hintOf(y))
	} else {
		x = c.expr(e.X, expected)
		y = c.expr(e.Y, hintOf(x))
	}

	if x.mode == invalid || y.mode == invalid {
		return invalidOperand
	}

	xt, yt := readType(x), readType(y)
	if !Identical(xt, yt) {
		c.errorf(e.OpPos, "mismatched types "+xt.String()+" and "+yt.String()+" in "+e.Op.String())
		return invalidOperand
	}

	var need BasicInfo
	result := xt
	switch e.Op {
	case token.LAND, token.LOR:
		need = IsBoolean
	case token.EQL, token.NEQ:
		result = Typ[Bool]
		if !comparable(xt) {
			c.errorf(e.OpPos, "values of type "+xt.String()+" cannot be compared with "+e.Op.String())
			return invalidOperand
		}

		return operand{value, result, nil, nil}
	case token.LSS, token.LEQ, token.GTR, token.GEQ:
		need = IsNumeric | IsString
		result = Typ[Bool]
	case token.ADD:
		need = IsNumeric | IsString
	case token.SUB, token.MUL, token.QUO:
		need = IsNumeric
	default: // %, &, |, ^, <<, >>, &^
		need = IsInteger
	}

	if !hasInfo(xt, need) {
		c.errorf(e.OpPos, "operator "+e.Op.String()+" is not defined on "+xt.String())
		return invalidOperand
	}

	return operand{value, result, nil, nil}
}

// coalesce checks x ?? else. x is an option or a Result, possibly
// through a borrow, in which case the result is the same borrow of the
// inside: cur ?? ... on a mut (*Node?) yields a mut *Node, and s.head
// ?? ... through a ref receiver yields a ref *Node. The else is a value
// of the inside's type, or a block or statement that leaves.
func (c *Checker) coalesce(e *ast.CoalesceExpr, expected Type) operand {
	x := c.expr(e.X, nil)
	if x.mode == invalid {
		c.node(e.Else, nil)
		return invalidOperand
	}

	inner, m := x.typ, x.mode
	through := borrowOf(m) // a place reached through a borrow
	if r, ok := inner.(*Ref); ok {
		through = r // a borrow variable
		inner = r.elem
	}

	var result Type
	switch t := inner.Underlying().(type) {
	case *Option:
		result = t.elem
	case *Result:
		result = t.val
	default:
		c.errorf(e.OpPos, "?? needs an option or a Result on its left, not "+x.typ.String())
		c.node(e.Else, nil)
		return invalidOperand
	}

	if through != nil && !isBorrow(x.typ) && IsCopy(result) {
		m = value // a Copy value reached through a borrow is read, not lent
	} else if through != nil {
		result = NewRef(result, through.mut)
		m = value
		if through.mut {
			c.accessExpr(e.X, mutAccess, e.X.Pos()) // the inside is lent mut, from what x holds
		}
	} else if m == variable && !IsCopy(result) {
		c.move(e.X) // the value inside leaves the place
	}

	f := c.forkAt()
	c.branchEnd(&f) // the path with a value moves nothing
	c.elseValue(e.Else, result)
	c.branchEnd(&f)
	c.joinAt(&f, true)
	return operand{m, result, nil, nil}
}

// elseValue checks the right side of ?? or an else: an expression of
// the given type, or a block or statement that diverges.
func (c *Checker) elseValue(n ast.Node, t Type) {
	switch n := n.(type) {
	case ast.Expr:
		c.assign(c.expr(n, t), t, n.Pos(), "else value")
	case *ast.BlockStmt:
		v := c.blockValue(n, t)
		if v.mode != invalid && !isNever(v.typ) {
			c.errorf(n.Pos(), "the block after ?? must return, break or continue, or end in a value of type "+t.String())
		}
	case ast.Stmt:
		if c.stmt(n) {
			c.dead = true
		}
	}
}

// try checks x?: the function's result type says which kind x must be.
func (c *Checker) try(e *ast.TryExpr) operand {
	x := c.expr(e.X, nil)
	if x.mode == invalid {
		return invalidOperand
	}

	if c.sig == nil {
		c.errorf(e.Question, "? is only allowed inside a function")
		return invalidOperand
	}

	xt, _ := deref(x)
	if x.mode == variable && !isBorrow(x.typ) && !IsCopy(xt) {
		c.move(e.X) // the value inside leaves the place
	}

	switch want := c.sig.results.Underlying().(type) {
	case *Option:
		if o, ok := xt.Underlying().(*Option); ok {
			return operand{value, o.elem, nil, nil}
		}

		if _, ok := xt.Underlying().(*Result); ok {
			c.errorf(e.Question, "cannot use ? on a Result in a function returning an option")
			return invalidOperand
		}
	case *Result:
		if r, ok := xt.Underlying().(*Result); ok {
			if !Identical(r.err, want.err) {
				c.errorf(e.Question, "? cannot pass on an error of type "+r.err.String()+" from a function returning "+c.sig.results.String())
			}

			return operand{value, r.val, nil, nil}
		}

		if _, ok := xt.Underlying().(*Option); ok {
			c.errorf(e.Question, "cannot use ? on an option in a function returning a Result")
			return invalidOperand
		}
	default:
		c.errorf(e.Question, "? is only allowed in a function returning an option or a Result")
		return invalidOperand
	}

	c.errorf(e.Question, "? needs an option or a Result on its left, not "+x.typ.String())
	return invalidOperand
}

// funcLit checks a closure: its body is checked as a function body with
// the closure's own signature, and names it captures resolve through
// the enclosing scopes like any name.
func (c *Checker) funcLit(e *ast.FuncLit) operand {
	sig := c.typ(e.Type).(*Signature)
	outer, regions, dead := c.sig, c.regions, c.dead
	c.sig, c.regions, c.dead = sig, nil, false
	c.funcBody(sig, nil, e.Type.Params, e.Body)
	c.sig, c.regions, c.dead = outer, regions, dead
	return operand{value, sig, nil, nil}
}

// ifExpr checks if c { a } else { b } as an expression: both branches
// yield a value of one type, and a branch that leaves fits any type.
func (c *Checker) ifExpr(e *ast.IfExpr, expected Type) operand {
	c.condition(e.Cond)
	f := c.forkAt()
	a := c.blockValue(e.Then, expected)
	c.branchEnd(&f)
	var b operand
	switch els := e.Else.(type) {
	case *ast.BlockStmt:
		b = c.blockValue(els, expected)
	case *ast.IfExpr:
		b = c.ifExpr(els, expected)
	}

	c.branchEnd(&f)
	c.joinAt(&f, true)
	return c.unify(a, b, e.If, "if")
}

// blockValue checks a block whose last statement may be an expression
// giving the block's value, as in an if expression or a match arm. A
// block that returns, breaks or continues has the type never; one that
// ends in an ordinary statement is ().
func (c *Checker) blockValue(b *ast.BlockStmt, expected Type) operand {
	c.openScope()
	defer c.closeScope()
	if len(b.List) == 0 {
		return operand{value, Unit, nil, nil}
	}

	last := len(b.List) - 1
	c.stmts(b.List[:last])
	if es, ok := b.List[last].(*ast.ExprStmt); ok {
		return c.expr(es.X, expected)
	}

	if c.stmt(b.List[last]) {
		c.dead = true
		return operand{value, Typ[Never], nil, nil}
	}

	return operand{value, Unit, nil, nil}
}

// unify gives two branches one type: a branch that never yields fits
// the other, and otherwise the two must be assignable one way.
func (c *Checker) unify(a, b operand, pos token.Pos, what string) operand {
	switch {
	case a.mode == invalid || b.mode == invalid:
		return invalidOperand
	case isNever(a.typ):
		return b
	case isNever(b.typ):
		return a
	case assignable(b, valueType(a)):
		return operand{value, valueType(a), nil, nil}
	case assignable(a, valueType(b)):
		return operand{value, valueType(b), nil, nil}
	}

	c.errorf(pos, what+" branches have different types: "+a.typ.String()+" and "+b.typ.String())
	return invalidOperand
}
