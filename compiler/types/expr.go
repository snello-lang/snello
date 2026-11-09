// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
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

	if op.mode == typeOperand && unparen(e) != c.callee && unparen(e) != c.wantType { // a type stands only before the parentheses of a conversion, or where make wants one
		c.errorf(e.Pos(), exprText(e)+" (type) is not an expression")
		return invalidOperand
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
		return c.coalesce(e)
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

	if e.Name == "super" {
		c.errorf(e.Pos(), "super must be followed by a method: super.Method()")
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
			c.flowUse(obj, e.Pos()) // a use keeps what the variable holds lent; assigning the whole variable is not one
		}

		c.capture(obj, byRef)
		c.checkUseVar(e, obj)
		return operand{variable, obj.typ, obj, nil}
	case *Const:
		if obj.typ == nil {
			return invalidOperand
		}

		return operand{value, obj.typ, obj, nil}
	case *Func:
		if sig, ok := obj.typ.(*Signature); ok && sig.tparams != nil && c.callee != e {
			c.errorf(e.Pos(), "cannot use the generic function "+obj.name+" without calling it")
			return invalidOperand
		}

		if c.callee != e {
			c.keepValueOf(obj, e.Pos()) // a value: it may keep no parameter
		}

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

	t := v.parent
	if t.tparams != nil {
		n, ok := expected.(*Named) // a variant without fields has nothing to infer from but its context
		if !ok || n.Origin() != t {
			if expected != Typ[Invalid] {
				c.errorf(e.Pos(), "cannot infer the type parameter "+t.tparams[0].obj.name+" of "+v.name+": the context gives it no type")
			}

			return invalidOperand
		}

		t = n
	}

	return operand{value, t, v, nil}
}

// selector checks x.Sel: a package member, a field, a method of a
// declared type, or a predeclared method. Fields and methods are found
// through any borrow or owned pointer, so n.value works whether n is a
// Node, a ref Node, or a *Node.
func (c *Checker) selector(e *ast.SelectorExpr) operand {
	if ident, ok := e.X.(*ast.Ident); ok {
		if ident.Name == "super" {
			return c.superCall(e)
		}

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
		if st, ok := named.Underlying().(*Struct); ok {
			if f := fieldNamed(st.fields, e.Sel.Name); f != nil {
				if !c.visible(named, e.Sel, f) {
					return invalidOperand
				}

				c.recordUse(e.Sel, f)
				c.checkUse(e)
				return operand{m, f.typ, f, nil}
			}
		}

		if fn := named.Method(e.Sel.Name); fn != nil {
			if !c.visible(named, e.Sel, nil) {
				return invalidOperand
			}

			c.recordUse(e.Sel, fn)
			sig := fn.typ.(*Signature)
			if sig.tparams != nil && c.callee != ast.Expr(e) { // the selector itself, which call unparenthesized
				c.errorf(e.Sel.Pos(), "cannot use the generic method "+fn.name+" without calling it")
				return invalidOperand
			}

			c.receiver(e, x, sig)
			if c.callee != ast.Expr(e) {
				c.keepValueOf(fn, e.Sel.Pos()) // a method as a value: it may keep no parameter
			}

			return operand{value, fn.typ, fn, nil}
		}
	}

	if sig := builtinMethod(base, e.Sel.Name); sig != nil {
		c.receiver(e, x, sig)
		return operand{value, sig, nil, nil}
	}

	// A trait value, or a type parameter with bounds: the traits' fields
	// and methods, with the value's type for Self.
	var f *Var
	var fn *Func
	var param *TypeParam // the parameter whose bounds were searched, for the message
	switch t := base.(type) {
	case *Trait:
		var mentionsSelf bool
		if f, mentionsSelf = t.valueField(e.Sel.Name); mentionsSelf {
			c.errorf(e.Sel.Pos(), "cannot reach "+e.Sel.Name+" through "+t.obj.name+": its type mentions Self")
			return invalidOperand
		}

		if f == nil {
			if fn, mentionsSelf = t.valueMethod(e.Sel.Name); mentionsSelf {
				c.errorf(e.Sel.Pos(), "cannot call "+e.Sel.Name+" through "+t.obj.name+": its signature mentions Self")
				return invalidOperand
			}
		}
	case *TypeParam:
		if f = t.boundField(e.Sel.Name); f == nil {
			fn = t.boundMethod(e.Sel.Name)
		}

		param = t
	}

	if f != nil {
		c.recordUse(e.Sel, f)
		c.checkUse(e)
		return operand{m, f.typ, f, nil}
	}

	if fn != nil {
		c.recordUse(e.Sel, fn)
		c.receiver(e, x, fn.typ.(*Signature))
		if c.callee != ast.Expr(e) {
			c.keepValueOf(fn, e.Sel.Pos()) // a method as a value: it may keep no parameter
		}

		return operand{value, fn.typ, fn, nil}
	}

	msg := e.Sel.Name + " is not a field or method of " + x.typ.String()
	if param != nil {
		msg += ": no bound of " + param.obj.name + " declares it"
	}

	c.errorf(e.Sel.Pos(), msg)
	return invalidOperand
}

// receiver checks the receiver of a method against what the method
// asks of it: a value receiver takes it (takeValue); a mut receiver
// needs a place that can be changed, which a value reached through a
// ref is not, nor a constant. The borrow the call creates is checked in
// call, after the arguments.
func (c *Checker) receiver(e *ast.SelectorExpr, x operand, sig *Signature) {
	r, ok := sig.recv.typ.(*Ref)
	if !ok {
		c.takeValue(e.X, x, sig.recv.typ)
		return
	}

	c.checkUse(e.X)

	if r.mut && x.mode == borrowed {
		c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut through a ref "+c.throughWhat(e.X))
	} else if r.mut && c.unchangeable(e.X, x) {
		c.errorf(e.X.Pos(), e.Sel.Name+" lends its receiver mut, which "+needsPlace(e.X))
	} else if r.mut && isBorrow(x.typ) && !isMut(x.typ) { // the receiver is itself a ref borrow
		c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut: "+exprText(e.X)+" is a ref "+c.throughWhat(e.X))
	}

	c.noteTemporary(e.X, x)
}

// packageMember resolves pkg.Name as a value: a function, variable,
// constant or type of the package.
func (c *Checker) packageMember(e *ast.SelectorExpr, pkg *PkgName) operand {
	obj := c.member(pkg, e.Sel)
	if obj == nil {
		return invalidOperand
	}

	switch obj := obj.(type) {
	case *Func:
		if c.callee != ast.Expr(e) {
			c.keepValueOf(obj, e.Pos()) // a value: it may keep no parameter
		}

		return operand{value, obj.typ, obj, nil}
	case *Var:
		return operand{variable, obj.typ, obj, nil}
	case *Const:
		return operand{value, obj.typ, obj, nil}
	case *TypeName:
		return operand{typeOperand, obj.typ, obj, nil}
	case *Variant:
		return c.constantVariant(e.Sel, obj, nil) // io.NotExist: a variant named through its package
	}

	c.errorf(e.Sel.Pos(), "cannot use "+pkg.name+"."+e.Sel.Name+" as a value")
	return invalidOperand
}

// visible reports whether the field or method sel of a value of type t
// may be named here: always inside the package that declares it, and
// elsewhere only when the name is exported, as in Go. A field is the
// package's that declared its struct, so a type declared over another
// package's struct does not open its fields. A name that is not
// visible is reported.
func (c *Checker) visible(t Type, sel *ast.Ident, field *Var) bool {
	n, ok := t.(*Named)
	if !ok || ast.IsExported(sel.Name) {
		return true
	}

	owner := n.obj.pkg
	if field != nil && field.pkg != nil {
		owner = field.pkg
	}

	if owner == nil || owner == c.pkg {
		return true
	}

	c.errorf(sel.Pos(), sel.Name+" of "+n.String()+" is not exported")
	return false
}

// qualified resolves pkg.Name written as a type, a trait or a pattern,
// and records the use of the package name; nil after reporting.
func (c *Checker) qualified(pkgIdent, sel *ast.Ident) Object {
	obj := c.lookup(pkgIdent)
	if obj == nil {
		return nil
	}

	pkg, ok := obj.(*PkgName)
	if !ok {
		c.errorf(pkgIdent.Pos(), pkgIdent.Name+" is not a package")
		return nil
	}

	return c.member(pkg, sel)
}

// packageVariant returns the variant pkg.Name names when the callee of
// a call is one, recording the uses; nil for anything else, which the
// call then resolves as a value and reports if it must.
func (c *Checker) packageVariant(fun ast.Expr) *Variant {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	_, obj := c.scope.LookupParent(id.Name)
	pkg, ok := obj.(*PkgName)
	if !ok || pkg.pkg == nil || !ast.IsExported(sel.Sel.Name) {
		return nil
	}

	v, ok := pkg.pkg.scope.Lookup(sel.Sel.Name).(*Variant)
	if !ok {
		return nil
	}

	c.recordUse(id, pkg)
	c.recordUse(sel.Sel, v)
	return v
}

// member resolves a name in an imported package and records the use:
// only an exported name is visible, as in Go. A member of a package the
// importer did not find is not reported: the import was.
func (c *Checker) member(pkg *PkgName, sel *ast.Ident) Object {
	if pkg.missing {
		return nil
	}

	var obj Object
	if pkg.pkg != nil {
		obj = pkg.pkg.scope.Lookup(sel.Name)
	}

	if obj != nil && !ast.IsExported(sel.Name) {
		c.errorf(sel.Pos(), "name "+sel.Name+" not exported by package "+pkg.name)
		return nil
	}

	if obj == nil {
		c.errorf(sel.Pos(), "undefined: "+pkg.name+"."+sel.Name)
		return nil
	}

	c.recordUse(sel, obj)
	return obj
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
		if c.noUse { // m[k] = v: the map keeps the key
			c.assign(c.operandOf(e.Index, t.key), t.key, e.Index.Pos(), "map key")
			if wasInner { // m[k].f = v writes part of an entry a missing key makes whole from the zero value
				c.zeroWrite(e, t)
			}
		} else {
			c.fits(c.operandOf(e.Index, t.key), t.key, e.Index.Pos(), "map key")
			c.zeroRead(e, t)
		}

		c.checkUse(e)
		return operand{m, t.value, nil, nil}
	case *Basic:
		if t.kind == String {
			c.intIndex(e.Index)
			c.checkUse(e)
			c.stringByte = e
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

// mutableView reports whether e is a slice of a value mut may lend:
// one owned, or reached through a mut borrow, not through a ref.
func (c *Checker) mutableView(e ast.Expr) bool {
	s, ok := unparen(e).(*ast.SliceExpr)
	return ok && !c.readOnly[s]
}

// sliceExpr checks x[lo:hi], which lends the piece: a ref string or a
// ref []T, never mut itself; mut x[lo:hi] lends it for changing when x
// is owned or reached through a mut borrow (mutableView).
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
	if m == borrowed {
		if c.readOnly == nil {
			c.readOnly = make(map[*ast.SliceExpr]bool)
		}

		c.readOnly[e] = true
	}

	switch t := base.Underlying().(type) {
	case *Slice:
		return operand{value, NewRef(t, false), nil, nil} // a view: it never owns or grows the elements, so it is never mut
	case *Basic:
		if t.kind == String {
			return operand{value, NewRef(Typ[String], false), nil, nil}
		}
	}

	c.errorf(e.Pos(), "cannot slice a value of type "+x.typ.String())
	return invalidOperand
}

// unchangeable reports whether x, checked as the operand op, can never
// change, so that lending it mut is a mistake: a constant, by its name,
// written as a literal or computed from constants, or a byte of a
// string. What is written through such a borrow goes to a copy and is
// lost. A call's result is a value made for the statement and may be
// lent: os.Stderr() is the stream itself.
func (c *Checker) unchangeable(x ast.Expr, op operand) bool {
	if op.mode != value {
		return false // a place, or what a borrow reaches
	}

	if _, ok := op.obj.(*Const); ok {
		return true
	}

	return unparen(x) == c.stringByte || c.constantExpr(x)
}

// constantExpr reports whether x is made of constants alone: a literal,
// the name of a constant, of this package or another, an operator over
// constants, -1 or (limit * 2), or a conversion of a constant,
// int(limit), which makes no value of its own.
func (c *Checker) constantExpr(x ast.Expr) bool {
	switch x := unparen(x).(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		_, ok := c.resolve(x.Name).(*Const)
		return ok
	case *ast.SelectorExpr:
		_, ok := c.packageObject(x).(*Const)
		return ok
	case *ast.UnaryExpr:
		return (x.Op == token.SUB || x.Op == token.NOT || x.Op == token.XOR) && c.constantExpr(x.X)
	case *ast.BinaryExpr:
		return c.constantExpr(x.X) && c.constantExpr(x.Y)
	case *ast.CallExpr:
		return len(x.Args) == 1 && c.isTypeName(x.Fun) && c.constantExpr(x.Args[0])
	}

	return false
}

// isTypeName reports whether x names a type, T or pkg.T, so that a call
// of it is a conversion.
func (c *Checker) isTypeName(x ast.Expr) bool {
	switch x := unparen(x).(type) {
	case *ast.Ident:
		_, ok := c.resolve(x.Name).(*TypeName)
		return ok
	case *ast.SelectorExpr:
		_, ok := c.packageObject(x).(*TypeName)
		return ok
	}

	return false
}

// packageObject returns what pkg.Name names in another package, or nil
// when x is not a name of an imported package's.
func (c *Checker) packageObject(x *ast.SelectorExpr) Object {
	id, ok := x.X.(*ast.Ident)
	if !ok {
		return nil
	}

	pkg, ok := c.resolve(id.Name).(*PkgName)
	if !ok || pkg.pkg == nil {
		return nil
	}

	return pkg.pkg.scope.Lookup(x.Sel.Name)
}

// needsPlace ends the refusal of mut of x, which unchangeable decided.
func needsPlace(x ast.Expr) string {
	return "needs a place, a variable or a field, element or *p of one: " + exprText(x) + " is not one"
}

func (c *Checker) unary(e *ast.UnaryExpr, expected Type) operand {
	switch e.Op {
	case token.REF, token.MUT:
		forBuiltin := c.placeFor
		c.placeFor = false // a mut inside the place is lent for itself
		x := c.expr(e.X, paramHint(expected))
		if x.mode == invalid {
			return invalidOperand
		}

		if e.Op == token.MUT && x.mode == borrowed {
			c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut through a ref "+c.throughWhat(e.X))
			return invalidOperand
		}

		if e.Op == token.MUT && !forBuiltin && c.unchangeable(e.X, x) { // take and replace refuse every value that is not a place, by their own name
			c.errorf(e.X.Pos(), "mut "+needsPlace(e.X))
			return invalidOperand
		}

		if e.Op == token.MUT && isBorrow(x.typ) && !isMut(x.typ) && !c.mutableView(e.X) { // a borrow of a borrow is a borrow of the value, which a ref may not change
			if s, ok := unparen(e.X).(*ast.SliceExpr); ok {
				c.errorf(e.X.Pos(), "cannot borrow "+exprText(s)+" as mut: it is a slice of "+exprText(s.X)+", reached through a ref "+c.throughWhat(s.X))
			} else {
				c.errorf(e.X.Pos(), "cannot borrow "+exprText(e.X)+" as mut: "+exprText(e.X)+" is a ref "+c.throughWhat(e.X))
			}

			return invalidOperand
		}

		t := x.typ
		if r, ok := t.(*Ref); ok {
			t = r.elem // a borrow of a borrow is a borrow of the value
		}

		c.lend(e.X, e.Op == token.MUT)
		c.lentMode = x.mode
		if isMut(x.typ) {
			c.lentMode = mutBorrowed // a mut borrow lent on lends the place it borrows
		}

		c.noteTemporary(e.X, x)
		if tmp := c.temporary[e.X]; tmp != nil {
			c.markTemporary(e, tmp) // ref s.Clone() borrows the clone
		}

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

		if ch, ok := readType(x).Underlying().(*Chan); ok { // every operation on a channel reads the handle through a borrow
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
	if fn := OperatorMethod(t, e.Op, true); fn != nil {
		sig := fn.typ.(*Signature)
		c.takeValue(e.X, x, sig.recv.typ)
		return operand{value, sig.results, nil, nil}
	}

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
		if x.mode == invalid {
			return invalidOperand
		}

		if fn := OperatorMethod(readType(x), e.Op, false); fn != nil {
			return c.operatorCall(e, x, fn)
		}

		if e.Op == token.LAND || e.Op == token.LOR {
			f := c.forkAt()
			c.branchEnd(&f) // the left decides, and the right is never evaluated
			y = c.expr(e.Y, hintOf(x))
			c.branchEnd(&f)
			c.joinAt(&f, true)
		} else {
			y = c.expr(e.Y, hintOf(x))
		}
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

// operatorCall checks a + b as a call of the operator's method on the
// left operand, a.Add(b): the receiver is taken by value, so it moves
// unless its type is Copy, as a value receiver does, and before the
// right operand is read, as a receiver is taken before the arguments,
// so a + a on a value that moves is a use after the move. The right
// operand takes its hint from the method's parameter, so v * 2 is a
// float when the method wants one, and must fit it; the result is the
// method's.
func (c *Checker) operatorCall(e *ast.BinaryExpr, x operand, fn *Func) operand {
	sig := fn.typ.(*Signature)
	c.takeValue(e.X, x, sig.recv.typ)
	param := sig.params[0].typ
	if y := c.expr(e.Y, param); y.mode != invalid {
		c.assign(y, param, e.Y.Pos(), "the right operand of "+e.Op.String())
	}

	return operand{value, sig.results, nil, nil}
}

// takeValue checks the operand xe, whose value a method takes as its
// receiver, by value, as recv: an owned value moves unless its type is
// Copy, and a borrow of a value that is not Copy cannot give the value
// up, since it is not the borrower's to give. The operand is read
// first, as any use is.
func (c *Checker) takeValue(xe ast.Expr, x operand, recv Type) {
	c.checkUse(xe)
	if isBorrow(x.typ) && !IsCopy(readType(x)) {
		c.errorf(xe.Pos(), "cannot move "+exprText(xe)+" out of a borrow")
		return
	}

	c.maybeMove(x, recv)
}

// coalesce checks x ?? else. x is an option or a Result, possibly
// through a borrow, in which case the result is the same borrow of the
// inside: cur ?? ... on a mut (*Node?) yields a mut *Node, and s.head
// ?? ... through a ref receiver yields a ref *Node. The else is a value
// of the inside's type, or a block or statement that leaves.
func (c *Checker) coalesce(e *ast.CoalesceExpr) operand {
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
	} else if held(e.X, m) && !IsCopy(result) {
		c.move(e.X, variable) // the value inside leaves the place
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
	if held(e.X, x.mode) && !isBorrow(x.typ) && !IsCopy(xt) {
		c.move(e.X, variable) // the value inside leaves the place
	}

	f := c.forkAt()
	c.branchEnd(&f) // the path with a value goes on
	c.flowReturnDrops(e.Question)
	c.dead = true // the other returns
	c.branchEnd(&f)
	c.joinAt(&f, true)

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
				c.passError(e, r.err, want.err)
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

// passError settles how x? hands an error of type from to a function
// returning one of a different type to: as a trait value when to is a
// trait from implements, or wrapped in the one variant of to's enum
// whose only field has type from, which Info.Wraps records for the
// backends. Two such variants are ambiguous, and anything else is
// refused with the reason.
func (c *Checker) passError(e *ast.TryExpr, from, to Type) {
	head := "? cannot pass on an error of type " + from.String() + " from a function returning " + c.sig.results.String()
	if tr, ok := to.(*Trait); ok {
		if !convertsToTrait(from, tr) {
			c.errorf(e.Question, head+traitReason(from, tr))
		}

		return
	}

	if _, ok := to.(*TypeParam); ok {
		c.errorf(e.Question, head+": "+to.String()+" is a type parameter")
		return
	}

	n, _ := to.(*Named)
	if n == nil {
		c.errorf(e.Question, head)
		return
	}

	en, ok := n.Underlying().(*Enum)
	if !ok {
		c.errorf(e.Question, head+": "+n.String()+" is not an enum")
		return
	}

	var holders []*Variant
	for _, v := range en.variants {
		if len(v.fields) == 1 && Identical(v.fields[0].typ, from) {
			holders = append(holders, v)
		}
	}

	switch len(holders) {
	case 1:
		if c.info != nil {
			c.info.Wraps[e] = holders[0]
		}
	case 0:
		c.errorf(e.Question, head+": no variant of "+n.String()+" holds "+article(from.String()))
	default:
		where := ""
		for i, v := range holders {
			if i > 0 {
				where += " and "
			}

			where += "in " + v.name
		}

		c.errorf(e.Question, head+": "+n.String()+" holds "+article(from.String())+" "+where+"; convert it")
	}
}

// article prefixes a type's name with a or an, for a message that
// speaks of one value of it.
func article(name string) string {
	if len(name) > 0 && isVowel(name[0]) {
		return "an " + name
	}

	return "a " + name
}

func isVowel(c byte) bool { c |= 0x20; return c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u' }

// funcLit checks a closure: its body is checked as a function body with
// the closure's own signature, and names it captures resolve through
// the enclosing scopes like any name.
func (c *Checker) funcLit(e *ast.FuncLit) operand {
	sig := c.typ(e.Type).(*Signature)
	outer, regions, dead := c.sig, c.regions, c.dead
	c.sig, c.regions, c.dead = sig, nil, false
	c.enclosing = append(c.enclosing, outer) // a parameter of the function around the closure may be kept from inside it
	c.closures = append(c.closures, closure{pos: e.Pos()})
	temps := c.temps
	c.temps = nil // the body runs when the closure is called, not while the call around it is lending
	mark := len(c.moved)
	c.floors = append(c.floors, floor{mark: mark})
	outerFlow := c.flow
	c.flow = c.newFlow(e.Pos()) // the body is a graph of its own: it runs when the closure is called
	c.flow.parent = outerFlow
	c.keepValues = append(c.keepValues, keepValue{sig: sig, pos: e.Pos(), closure: true}) // a closure is a value: it may keep no parameter
	c.funcBody(sig, nil, e.Type.Params, e.Body)
	c.solveFlow()
	c.freeFlow(c.flow)
	c.flow = outerFlow
	c.floors = c.floors[:len(c.floors)-1]
	c.moved = appendMoves(c.moved[:mark], c.moved[mark:]) // the body may never run: its moves count, as the captures do, but what it gives back does not
	c.temps = temps
	if caps := c.closures[len(c.closures)-1].captures; caps != nil {
		if c.captured == nil {
			c.captured = make(map[*ast.FuncLit][]capture)
		}

		c.captured[e] = caps // the closure is a borrow of what it captures
		if c.info != nil {
			vars := make([]Capture, len(caps))
			for i, cap := range caps {
				vars[i] = Capture{cap.v, cap.mode == byCopy}
			}

			c.info.Captures[e] = vars
		}
	}

	c.closures = c.closures[:len(c.closures)-1]
	c.enclosing = c.enclosing[:len(c.enclosing)-1]
	c.sig, c.regions, c.dead = outer, regions, dead
	for _, cap := range c.captured[e] {
		b := cap.borrow(nil, e.Pos())
		what := refAccess
		switch cap.mode {
		case byCopy:
			continue
		case byMut:
			what = mutAccess
		case byMove:
			what = moveAccess // made here, the closure owns what it moves
		}

		c.access(&ast.Ident{NamePos: e.Pos(), Name: cap.v.name}, b.place, what, e.Pos()) // made here, the closure borrows what it captures; named for the message
	}

	return operand{value, sig, nil, nil}
}

// ifExpr checks if c { a } else { b } as an expression: both branches
// yield a value of one type, and a branch that leaves fits any type.
func (c *Checker) ifExpr(e *ast.IfExpr, expected Type) operand {
	c.condition(e.Cond)
	f := c.forkAt()
	a := c.blockValue(e.Then, expected)
	c.maybeMove(a, a.typ) // the branch's value leaves with the if
	c.branchEnd(&f)
	var b operand
	switch els := e.Else.(type) {
	case *ast.BlockStmt:
		b = c.blockValue(els, expected)
		c.maybeMove(b, b.typ)
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
	defer c.endScope(b.Rbrace)
	if len(b.List) == 0 {
		return operand{value, Unit, nil, nil}
	}

	last := len(b.List) - 1
	c.stmts(b.List[:last])
	if es, ok := b.List[last].(*ast.ExprStmt); ok {
		op := c.expr(es.X, expected)
		c.pin(es.X, op.typ) // what takes the value finds the block's names after the block ends
		return op
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
