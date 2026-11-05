// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// call checks a conversion, a built-in, a variant constructor, or a
// call of a function or method value.
func (c *Checker) call(e *ast.CallExpr, expected Type) operand {
	lent := len(c.temps)
	op := c.callInner(e, expected)
	c.temps = c.temps[:lent] // the call returned; what it lent is given back
	return op
}

func (c *Checker) callInner(e *ast.CallExpr, expected Type) operand {
	if ident, ok := e.Fun.(*ast.Ident); ok {
		if _, obj := c.scope.LookupParent(ident.Name); obj != nil {
			switch obj := obj.(type) {
			case *Builtin:
				c.recordUse(ident, obj)
				if len(e.TypeArgs) > 0 {
					c.errorf(e.Lparen, obj.name+" takes no type arguments")
				}

				return c.builtinCall(e, obj)
			case *Variant:
				c.recordUse(ident, obj)
				return c.variantCall(e, obj, expected)
			}
		}
	}

	if v := c.packageVariant(e.Fun); v != nil {
		return c.variantCall(e, v, expected)
	}

	callee := c.callee
	c.callee = unparen(e.Fun)
	f := c.expr(e.Fun, nil)
	c.callee = callee // an inner call set its own: f().With(1) is a call of With
	if f.mode == invalid {
		c.exprs(e.Args)
		return invalidOperand
	}

	if f.mode == typeOperand {
		return c.conversion(e, f.typ)
	}

	sig, ok := f.typ.(*Signature)
	if !ok {
		c.errorf(e.Lparen, "cannot call a value of type "+f.typ.String())
		c.exprs(e.Args)
		return invalidOperand
	}

	if e == c.spawning {
		c.spawned(e, f, sig)
	}

	var tmp ast.Expr
	switch {
	case sig.tparams != nil:
		if sig, tmp = c.genericCall(e, sig, expected); sig == nil {
			return invalidOperand
		}
	case len(e.TypeArgs) > 0:
		c.errorf(e.Lparen, exprText(e.Fun)+" is not generic and takes no type arguments")
		c.exprs(e.Args)
		return invalidOperand
	default:
		tmp = c.args(e, sig)
	}

	if at, ok := formatted(f.obj); ok {
		c.printFormat(e, exprText(e.Fun), at, c.variadic)
	}

	if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sig.recv != nil {
		if n, ok := receiverBase(sig.recv.typ).(*Named); ok && n.orig != nil {
			c.recordCall(n.orig.tparams, n.targs, e.Lparen, n.obj.name) // the method is an instance's, made per type argument
		}

		if r, ok := sig.recv.typ.(*Ref); ok {
			c.lend(sel.X, r.mut, e.Rparen) // the receiver is borrowed last, after the arguments
			if t := c.temporary[sel.X]; t != nil {
				tmp = t
			}
		}
	}

	if _, ok := borrowIn(sig.results); ok && tmp != nil {
		c.markTemporary(e, tmp) // the result borrows what the receiver or argument did
	}

	if diverges(f.obj) {
		return operand{value, Typ[Never], nil, nil}
	}

	return operand{value, sig.results, nil, nil}
}

// args checks the arguments of a call against the parameters. An
// argument for a ref parameter is checked with the borrowed type as its
// hint, so a literal or None gets the type the parameter holds. It
// returns the temporary a borrowed argument lends, if any.
func (c *Checker) args(e *ast.CallExpr, sig *Signature) (tmp ast.Expr) {
	params := sig.params
	fixed := len(params)
	if sig.variadic {
		fixed--
	}

	if len(e.Args) < fixed || (!sig.variadic && len(e.Args) > fixed) {
		c.errorf(e.Lparen, "wrong number of arguments: have "+strconv.Itoa(len(e.Args))+", want "+strconv.Itoa(fixed))
		c.exprs(e.Args)
		return nil
	}

	var rest []operand
	defer func() { c.variadic = rest }() // after the arguments' own calls, which set it too
	for i, arg := range e.Args {
		if i >= fixed {
			op := c.expr(arg, nil) // a variadic any parameter takes anything
			c.keepLent(arg, op)
			rest = append(rest, op)
			continue
		}

		p := params[i]
		op := c.expr(arg, paramHint(p.typ))
		if !c.fits(op, p.typ, arg.Pos(), "argument") {
			continue
		}

		c.keepLent(arg, op)

		if t := c.argEffects(e, arg, op, p); t != nil {
			tmp = t
		}
	}

	return tmp
}

// keepLent keeps lent, until the call an argument is passed to returns,
// what the argument borrows when it is a borrow another call returned:
// that call gave back what it lent, but its result still points into
// the source, so two(a.Get(), clear(mut a)) would change a under the
// borrow passed beside it. A variable holds its own borrow, and ref x
// lends for the call already.
func (c *Checker) keepLent(arg ast.Expr, op operand) {
	if op.mode == invalid {
		return
	}

	mut, ok := borrowIn(op.typ)
	if !ok {
		return
	}

	switch unparen(arg).(type) {
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.UnaryExpr, *ast.FuncLit:
		return
	}

	for _, src := range c.borrowSources(arg) {
		c.lend(src, mut, arg.Pos())
	}
}

// diverges reports whether a call of obj never returns: os.Exit, which
// the library declares without a body and the compiler provides, ends
// the program as panic does, and the checker knows it as it knows
// panic, since a function cannot say so in its signature yet.
func diverges(obj Object) bool {
	fn, ok := obj.(*Func)
	return ok && fn.name == "Exit" && fn.pkg != nil && fn.pkg.path == "os"
}

// genericVariant checks a variant of a generic enum: its type
// arguments come from the type the context expects, the arguments
// written, Leaf<int>(1), and the values, in that order.
func (c *Checker) genericVariant(e *ast.CallExpr, v *Variant, expected Type) operand {
	if expected == Typ[Invalid] {
		c.exprs(e.Args)
		return invalidOperand
	}

	if len(e.Args) != len(v.fields) {
		c.errorf(e.Lparen, v.name+" has "+strconv.Itoa(len(v.fields))+" fields, not "+strconv.Itoa(len(e.Args)))
		c.exprs(e.Args)
		return invalidOperand
	}

	in := newInference(v.parent.tparams)
	if !c.explicit(in, e.TypeArgs, v.name, e.Fun, nil) {
		c.exprs(e.Args)
		return invalidOperand
	}

	in.unify(v.parent, expected)
	slots := varTypes(v.fields)
	ops := c.evaluate(in, e.Args, slots, func(i int, op operand) { c.maybeMove(op, slots[i]) })
	targs, ok := c.complete(in, v.name, e.Fun, nil)
	if !ok {
		return invalidOperand
	}

	t := Instantiate(v.parent, targs)
	fields := t.Underlying().(*Enum).variants[v.index].fields
	for i, op := range ops {
		c.fits(op, fields[i].typ, e.Args[i].Pos(), "field "+fields[i].name)
	}

	return operand{value, t, nil, nil}
}

// argEffects runs what passing op for the parameter p does, once the
// argument fits: a kept or spawned argument may not be a borrow, an
// argument for a ref or mut parameter is lent for the call, and any
// other is moved unless it is Copy. It returns the temporary the
// argument lends, if any. Only the parameter's shape matters, so a
// generic call runs it before its type arguments are known.
func (c *Checker) argEffects(e *ast.CallExpr, arg ast.Expr, op operand, p *Var) (tmp ast.Expr) {
	switch {
	case p.kept:
		c.kept(op, "stored", arg.Pos())
	case e == c.spawning:
		c.kept(op, "spawned", arg.Pos())
		c.spawnArg(arg, op)
	}

	if _, ok := p.typ.(*Ref); ok {
		c.noteTemporary(arg, op)
		tmp = c.temporary[arg]
	}

	if r, ok := p.typ.(*Ref); ok && op.mode != value {
		c.lend(arg, r.mut, arg.Pos()) // an owned place or a borrow variable is lent for the call
	} else if !isMut(op.typ) {
		c.lendCaptures(arg, arg.Pos()) // a closure lends what it captures for the call
		c.maybeMove(op, p.typ)
	}

	return tmp
}

// genericCall checks the arguments of a call of a generic function or
// method: the type arguments written at the call come first, the rest
// are inferred from the arguments and then from the type the context
// expects of the result, and the arguments are checked against the
// signature with them in place, which it returns, nil after an error,
// with the temporary an argument lends. The instance is recorded for
// the backend.
func (c *Checker) genericCall(e *ast.CallExpr, sig *Signature, expected Type) (*Signature, ast.Expr) {
	name := exprText(e.Fun)
	in := newInference(sig.tparams)
	if !c.explicit(in, e.TypeArgs, name, e.Fun, sig) {
		c.exprs(e.Args)
		return nil, nil
	}

	fixed := len(sig.params)
	if sig.variadic {
		fixed--
	}

	if len(e.Args) < fixed || (!sig.variadic && len(e.Args) > fixed) {
		c.errorf(e.Lparen, "wrong number of arguments: have "+strconv.Itoa(len(e.Args))+", want "+strconv.Itoa(fixed))
		c.exprs(e.Args)
		return nil, nil
	}

	var tmp ast.Expr
	ops := c.evaluate(in, e.Args[:fixed], varTypes(sig.params[:fixed]), func(i int, op operand) {
		if t := c.argEffects(e, e.Args[i], op, sig.params[i]); t != nil {
			tmp = t
		}
	})
	var rest []operand
	for _, arg := range e.Args[fixed:] {
		rest = append(rest, c.expr(arg, nil)) // a variadic any parameter takes anything
	}

	c.variadic = rest
	in.unify(sig.results, expected)
	targs, ok := c.complete(in, name, e.Fun, sig)
	if !ok {
		return nil, nil
	}

	inst := in.apply(sig).(*Signature)
	for i, op := range ops {
		c.fits(op, inst.params[i].typ, e.Args[i].Pos(), "argument")
	}

	c.recordCall(sig.tparams, targs, e.Lparen, name)

	if id := calleeName(e.Fun); id != nil && c.info != nil {
		c.info.Instances[id] = Instance{targs, inst}
	}

	return inst, tmp
}

// calleeName is the name a call's function is written with: f, or the
// method's name in x.f, through any parentheses. A generic function or
// method reaches a call only through one of these, since it cannot be
// a value.
func calleeName(fun ast.Expr) *ast.Ident {
	switch fun := unparen(fun).(type) {
	case *ast.SelectorExpr:
		return fun.Sel
	case *ast.Ident:
		return fun
	}

	return nil
}

// paramHint is the type to check an argument with: the type behind a
// ref or mut parameter, so that the argument's own expression types
// itself, and the borrow is checked by assign.
func paramHint(t Type) Type {
	if r, ok := t.(*Ref); ok {
		return r.elem
	}

	return t
}

// conversion checks T(x): between number types, between a declared
// type and its underlying type, and between string and []byte.
func (c *Checker) conversion(e *ast.CallExpr, t Type) operand {
	if len(e.Args) != 1 {
		c.errorf(e.Lparen, "a conversion takes one argument")
		c.exprs(e.Args)
		return invalidOperand
	}

	x := c.expr(e.Args[0], t)
	if x.mode == invalid || t == Typ[Invalid] {
		return operand{value, t, nil, nil}
	}

	v := valueType(x)
	if r, ok := x.typ.(*Ref); ok && (isString(r.elem) || isByteSlice(r.elem)) {
		v = r.elem // a conversion between a string and its bytes copies, so it reads a borrowed operand as well
	}

	switch {
	case hasInfo(v, IsNumeric) && hasInfo(t, IsNumeric):
	case Identical(v.Underlying(), t.Underlying()):
	case isString(v) && isByteSlice(t), isByteSlice(v) && isString(t):
	default:
		c.errorf(e.Args[0].Pos(), "cannot convert "+x.typ.String()+" to "+t.String())
	}

	return operand{value, t, nil, nil}
}

func (c *Checker) builtinCall(e *ast.CallExpr, b *Builtin) operand {
	switch b.id {
	case BuiltinLen, BuiltinCap:
		return c.lenCall(e, b)
	case BuiltinMake:
		return c.makeCall(e)
	case BuiltinTake:
		return c.takeCall(e)
	case BuiltinPanic:
		c.exprs(e.Args)
		return operand{value, Typ[Never], nil, nil}
	case BuiltinClose:
		return c.closeCall(e)
	case BuiltinMin, BuiltinMax:
		return c.minCall(e, b)
	}

	for _, arg := range e.Args { // print
		c.keepLent(arg, c.expr(arg, nil))
	}

	return operand{value, Unit, nil, nil}
}

// minCall checks min(a, b, ...) and max(a, b, ...): two or more numbers
// of one type, which is the result's. A literal among them takes the
// type of the others.
func (c *Checker) minCall(e *ast.CallExpr, b *Builtin) operand {
	if len(e.Args) < 2 {
		c.errorf(e.Lparen, b.name+" takes at least two arguments")
		c.exprs(e.Args)
		return invalidOperand
	}

	// The first argument that types itself gives the type; the literals
	// come last and take it, or the first literal's own when there is
	// nothing else.
	var t Type
	for _, late := range []bool{false, true} {
		for _, a := range e.Args {
			if contextTyped(a) != late {
				continue
			}

			hint := t
			if hint == nil && late && anyFloatLit(e.Args) {
				hint = Typ[Float64] // literals alone: a point among them makes them all f64, as Go has it
			}

			x := c.expr(a, hint)
			if x.mode == invalid {
				return invalidOperand
			}

			if t != nil {
				c.assign(x, t, a.Pos(), "argument")
				continue
			}

			t = readType(x)
			if u, ok := t.Underlying().(*Basic); !ok || u.info&IsNumeric == 0 {
				c.errorf(a.Pos(), b.name+" takes numbers, not "+t.String())
				return invalidOperand
			}
		}
	}

	return operand{value, t, nil, nil}
}

// anyFloatLit reports whether one of the expressions is a float literal,
// through parentheses and a sign.
func anyFloatLit(es []ast.Expr) bool {
	for _, e := range es {
		for {
			switch x := e.(type) {
			case *ast.ParenExpr:
				e = x.X
				continue
			case *ast.UnaryExpr:
				e = x.X
				continue
			case *ast.BasicLit:
				if x.Kind == token.FLOAT {
					return true
				}
			}

			break
		}
	}

	return false
}

// lenCall checks len(x) and cap(x): len on a slice, array, map, string
// or channel, cap on a slice, array or channel.
func (c *Checker) lenCall(e *ast.CallExpr, b *Builtin) operand {
	if len(e.Args) != 1 {
		c.errorf(e.Lparen, b.name+" takes one argument")
		c.exprs(e.Args)
		return invalidOperand
	}

	x := c.expr(e.Args[0], nil)
	if x.mode == invalid {
		return operand{value, Typ[Int], nil, nil}
	}

	base, _ := deref(x)
	ok := false
	switch base.Underlying().(type) {
	case *Slice, *Array, *Chan:
		ok = true
	case *Map, *Basic:
		ok = b.id == BuiltinLen && (isString(base) || isMap(base))
	}

	if !ok {
		c.errorf(e.Args[0].Pos(), b.name+" is not defined on "+x.typ.String())
	}

	return operand{value, Typ[Int], nil, nil}
}

// makeCall checks make(T, n...): T is a slice, map or channel type.
func (c *Checker) makeCall(e *ast.CallExpr) operand {
	if len(e.Args) == 0 {
		c.errorf(e.Lparen, "make needs a type")
		return invalidOperand
	}

	c.wantType = unparen(e.Args[0])
	t := c.expr(e.Args[0], nil)
	for _, arg := range e.Args[1:] {
		c.intIndex(arg)
	}

	if t.mode == invalid {
		return invalidOperand // a type that was refused, reported there
	}

	if t.mode != typeOperand {
		c.errorf(e.Args[0].Pos(), "make needs a type, not a value")
		return invalidOperand
	}

	switch t.typ.Underlying().(type) {
	case *Slice, *Map, *Chan:
		return operand{value, t.typ, nil, nil}
	}

	c.errorf(e.Args[0].Pos(), "make builds a slice, map or channel, not "+t.typ.String())
	return invalidOperand
}

// takeCall checks take(mut place): the argument is written mut, the
// place's type has a zero value to leave behind, and the result is the
// place's value.
func (c *Checker) takeCall(e *ast.CallExpr) operand {
	if len(e.Args) != 1 {
		c.errorf(e.Lparen, "take takes one argument: take(mut place)")
		c.exprs(e.Args)
		return invalidOperand
	}

	u, ok := e.Args[0].(*ast.UnaryExpr)
	if !ok || u.Op != token.MUT {
		c.expr(e.Args[0], nil)
		c.errorf(e.Args[0].Pos(), "take moves a value out of a place written mut: take(mut x.f)")
		return invalidOperand
	}

	x := c.expr(e.Args[0], nil)
	if x.mode == invalid {
		return invalidOperand
	}

	t := x.typ.(*Ref).elem
	if !hasZero(t) {
		c.errorf(e.Args[0].Pos(), "cannot take "+exprText(u.X)+": "+t.String()+" has no zero value to leave behind")
		return invalidOperand
	}

	return operand{value, t, nil, nil}
}

// variantCall checks Some(x), Ok(x), Err(e) and a declared variant with
// fields. The built-in three take their type from the context when it
// has one, else from the argument; Ok and Err need the context, since
// the argument says nothing about the other half.
func (c *Checker) variantCall(e *ast.CallExpr, v *Variant, expected Type) operand {
	if v.parent != nil && v.parent.tparams != nil {
		return c.genericVariant(e, v, expected)
	}

	if len(e.TypeArgs) > 0 {
		c.errorf(e.Lparen, v.name+" is not generic and takes no type arguments")
		c.exprs(e.Args)
		return invalidOperand
	}

	if v.parent != nil {
		t := v.parent
		if len(e.Args) != len(v.fields) {
			c.errorf(e.Lparen, v.name+" has "+strconv.Itoa(len(v.fields))+" fields, not "+strconv.Itoa(len(e.Args)))
			c.exprs(e.Args)
			return operand{value, t, nil, nil}
		}

		for i, arg := range e.Args {
			c.assign(c.expr(arg, v.fields[i].typ), v.fields[i].typ, arg.Pos(), "field "+v.fields[i].name)
		}

		return operand{value, t, nil, nil}
	}

	if len(e.Args) != 1 {
		c.errorf(e.Lparen, v.name+" takes one argument")
		c.exprs(e.Args)
		return invalidOperand
	}

	if expected == Typ[Invalid] {
		c.exprs(e.Args)
		return invalidOperand
	}

	var hint Type
	if expected != nil {
		switch t := expected.Underlying().(type) {
		case *Option:
			if v == SomeVariant {
				hint = t.elem
			}
		case *Result:
			switch v {
			case OkVariant:
				hint = t.val
			case ErrVariant:
				hint = t.err
			}
		}
	}

	x := c.expr(e.Args[0], hint)
	if x.mode == invalid {
		return invalidOperand
	}

	if hint != nil {
		c.assign(x, hint, e.Args[0].Pos(), "argument")
		return operand{value, expected, nil, nil}
	}

	switch v {
	case SomeVariant:
		c.maybeMove(x, valueType(x)) // the value moves into the option
		return operand{value, NewOption(valueType(x)), nil, nil}
	case NoneVariant:
		c.errorf(e.Lparen, "None takes no argument")
		return invalidOperand
	}

	c.errorf(e.Lparen, "cannot tell the type of "+v.name+"(...) here; the context gives it no Result type")
	return invalidOperand
}
