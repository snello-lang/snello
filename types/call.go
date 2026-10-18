// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strconv"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
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
	for _, t := range e.TypeArgs {
		c.typ(t)
		c.errorf(e.Lparen, "explicit type arguments are not part of core Teak")
	}

	if ident, ok := e.Fun.(*ast.Ident); ok {
		if _, obj := c.scope.LookupParent(ident.Name); obj != nil {
			switch obj := obj.(type) {
			case *Builtin:
				c.recordUse(ident, obj)
				return c.builtinCall(e, obj)
			case *Variant:
				c.recordUse(ident, obj)
				return c.variantCall(e, obj, expected)
			}
		}
	}

	f := c.expr(e.Fun, nil)
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

	c.args(e, sig)
	if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sig.recv != nil {
		if r, ok := sig.recv.typ.(*Ref); ok {
			c.lend(sel.X, r.mut, e.Rparen) // the receiver is borrowed last, after the arguments
		}
	}

	return operand{value, sig.results, nil, nil}
}

// args checks the arguments of a call against the parameters. An
// argument for a ref parameter is checked with the borrowed type as its
// hint, so a literal or None gets the type the parameter holds.
func (c *Checker) args(e *ast.CallExpr, sig *Signature) {
	params := sig.params
	fixed := len(params)
	if sig.variadic {
		fixed--
	}

	if len(e.Args) < fixed || (!sig.variadic && len(e.Args) > fixed) {
		c.errorf(e.Lparen, "wrong number of arguments: have "+strconv.Itoa(len(e.Args))+", want "+strconv.Itoa(fixed))
		c.exprs(e.Args)
		return
	}

	for i, arg := range e.Args {
		if i >= fixed {
			c.expr(arg, nil) // a variadic any parameter takes anything
			continue
		}

		p := params[i]
		op := c.expr(arg, paramHint(p.typ))
		if !c.fits(op, p.typ, arg.Pos(), "argument") {
			continue
		}

		switch {
		case p.kept:
			c.kept(op, "stored", arg.Pos())
		case e == c.spawning:
			c.kept(op, "spawned", arg.Pos())
		}

		if r, ok := p.typ.(*Ref); ok && op.mode != value {
			c.lend(arg, r.mut, arg.Pos()) // an owned place or a borrow variable is lent for the call
		} else if !isMut(op.typ) {
			c.lendCaptures(arg, arg.Pos()) // a closure lends what it captures for the call
			c.maybeMove(op, p.typ)
		}
	}
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
	}

	c.exprs(e.Args) // print
	return operand{value, Unit, nil, nil}
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

// takeCall checks take(mut place): the argument is written mut, and the
// result is the place's value.
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

	return operand{value, x.typ.(*Ref).elem, nil, nil}
}

// variantCall checks Some(x), Ok(x), Err(e) and a declared variant with
// fields. The built-in three take their type from the context when it
// has one, else from the argument; Ok and Err need the context, since
// the argument says nothing about the other half.
func (c *Checker) variantCall(e *ast.CallExpr, v *Variant, expected Type) operand {
	if v.parent != nil {
		if len(e.Args) != len(v.fields) {
			c.errorf(e.Lparen, v.name+" has "+strconv.Itoa(len(v.fields))+" fields, not "+strconv.Itoa(len(e.Args)))
			c.exprs(e.Args)
			return operand{value, v.parent, nil, nil}
		}

		for i, arg := range e.Args {
			c.assign(c.expr(arg, v.fields[i].typ), v.fields[i].typ, arg.Pos(), "field "+v.fields[i].name)
		}

		return operand{value, v.parent, nil, nil}
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
			if v == OkVariant {
				hint = t.val
			} else if v == ErrVariant {
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
