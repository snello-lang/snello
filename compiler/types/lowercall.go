// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The lowering of calls: what a call names, what it takes and in which
// order, the receiver lent last when it is an owned place.

// A callee is what a call names, resolved once per call: the object its
// name refers to, when it has one, and the signature the call runs.
type callee struct {
	obj    Object     // the function, method, builtin, variant, part or type named; nil for a value called or a type written out
	named  bool       // the callee is a name, bare or selected
	isType bool       // a conversion written as T(x) with T a type expression
	sig    *Signature // what runs: the instance's for a generic function, the callee's type otherwise; nil for a builtin, a variant or a conversion
}

// calledBy resolves what the call x names, by what the typing recorded.
func (c *Checker) calledBy(x *ast.CallExpr) callee {
	var k callee
	id := calleeName(x.Fun)
	if id == nil {
		_, k.isType = unparen(x.Fun).(*ast.TypeExpr)
	} else {
		k.named, k.obj = true, c.info.Uses[id]
		if inst, ok := c.info.Instances[id]; ok {
			k.sig = inst.Type
			return k
		}
	}

	switch k.obj.(type) {
	case *Builtin, *Variant, *Part, *TypeName:
		return k
	}

	k.sig, _ = c.info.Types[x.Fun].(*Signature)
	return k
}

// names reports whether the callee is a name, of a function, a method,
// a builtin, a variant or a type, which is not evaluated.
func (k callee) names() bool {
	switch k.obj.(type) {
	case *Func, *Builtin, *Variant, *Part, *TypeName:
		return true
	case nil:
		return k.isType || k.named && k.sig != nil // a method the language declares, which no object stands for
	}

	return false
}

// makes reports whether the call makes a value rather than running a
// function: a variant's constructor or a conversion.
func (k callee) makes() bool {
	switch k.obj.(type) {
	case *Variant, *Part, *TypeName:
		return true
	}

	return k.isType
}

// conversion reports whether the call is a conversion, T(x).
func (k callee) conversion() bool {
	_, isType := k.obj.(*TypeName)
	return isType || k.isType
}

// builtin returns the builtin the call names, or nil.
func (k callee) builtin() *Builtin {
	b, _ := k.obj.(*Builtin)
	return b
}

// call lowers a call: the callee read when it is a place, the receiver
// read, the arguments left to right, each given to its parameter, the
// receiver lent last, and the call into a temporary. A variant written
// as a call and a conversion make their value from the arguments.
func (l *lowering) call(x *ast.CallExpr) Operand {
	k := l.c.calledBy(x)
	args, recv := l.callArgs(x, k)
	t := l.typeOf(x)
	tmp := l.temp(t, true, x)
	if src := callSourceIndex(k.sig, recv); src >= 0 && src < len(args) && holdsBorrow(t) {
		l.derives(tmp, args[src]) // a result that borrows from a temporary is of that temporary
	}

	op := OpCall
	if k.makes() {
		op = OpMake
	}

	l.emit(Instr{Op: op, To: tmp, Args: args, Expr: x, Pos: x.Pos()})
	if isNever(t) { // a call that never returns ends the function: what every open scope declares is dropped
		l.leave(0, x.Pos(), x)
	}

	return tmp
}

// callSourceIndex is the operand a call's result borrows from, among the
// operands callArgs gives, the receiver first when recv says there is
// one: a receiver that is a borrow, or the argument of the one borrowed
// parameter; -1 for none. A receiver taken by value dies with the call,
// so no returned borrow is of it.
func callSourceIndex(sig *Signature, recv bool) int {
	if sig == nil {
		return -1
	}

	if recv && isBorrow(sig.recv.typ) {
		return 0
	}

	i := borrowedParam(sig)
	if i >= 0 && recv {
		i++
	}

	return i
}

// callArgs evaluates what a call takes, in order, and returns the
// operands, the receiver's lend first when the callee is a method, and
// whether there is one.
func (l *lowering) callArgs(x *ast.CallExpr, k callee) ([]Operand, bool) {
	if k.conversion() && len(x.Args) == 1 {
		return l.args(l.converted(x, x.Args[0])), false
	}

	if !k.names() {
		if x == l.spawning && !isFuncLit(x.Fun) {
			l.keep(x.Fun, "spawned", x.Fun.Pos()) // the task runs the closure value: one that borrows cannot go; a closure written here moves what it captures instead
		}

		if p, ok := l.placeOf(x.Fun); ok { // a closure held in a variable or a field is read
			l.emit(Instr{Op: OpRead, Place: p, Expr: x.Fun, Pos: x.Fun.Pos()})
		} else {
			l.take(l.value(x.Fun, nil)) // a closure written here, or a call that gives a function: made before the arguments
		}
	}

	start := len(l.argStack)
	var recvPlace Operand
	var recvExpr ast.Expr
	var recv *Var
	if sel, ok := unparen(x.Fun).(*ast.SelectorExpr); ok && k.sig != nil && k.sig.recv != nil && !l.isPkgSelector(sel) {
		recv, recvExpr = k.sig.recv, sel.X
		l.argStack = append(l.argStack, Operand{}) // the receiver's place among the operands, filled once it is lent
		if p, ok := l.placeOf(sel.X); ok && isBorrow(recv.typ) {
			recvPlace = p
			l.emit(Instr{Op: OpRead, Place: p, Expr: sel.X, Pos: sel.X.Pos()}) // the receiver is read before the arguments, and lent after them
		} else {
			recvPlace = l.value(sel.X, recv.typ) // a part of a value made here is lent now, a borrow made here is given, a value made here is lent after the arguments and stays the statement's
			if !isBorrow(recv.typ) || l.isBorrowTemp(recvPlace) {
				l.take(recvPlace)
				recv = nil // given, not lent after the arguments
			}
		}
	}

	builtin := k.builtin()
	var kind VariadicKind
	var each Type
	if k.sig != nil && k.sig.variadic {
		kind, each = k.sig.variadicArg()
	}

	for i, arg := range x.Args {
		if builtin != nil && !(builtin.id == BuiltinReplace && i == 1) { // a builtin reads its arguments, but the value replace puts in the place
			a := l.read(arg)
			if l.isBorrowTemp(a) {
				l.take(a) // lent for the call
			}

			l.argStack = append(l.argStack, a)
			continue
		}

		var want Type
		var param *Var
		packed := false
		if k.sig != nil && (i < k.sig.fixed() || k.sig.variadic) {
			param, want, _, packed = argSlot(k.sig, i, x.Ellipsis.IsValid(), kind, each)
		}

		l.keepArg(arg, i, k, param, packed || builtin != nil && builtin.id == BuiltinReplace && i == 1, x == l.spawning)

		if r, ok := want.(*Ref); ok && isBorrow(l.typeOf(arg)) { // a borrow variable passed on is lent again, not moved: a borrow of the variable for the call
			if p, ok := l.placeOrPart(arg); ok {
				l.emit(Instr{Op: OpRead, Place: p, Expr: arg, Pos: arg.Pos()})
				l.argStack = append(l.argStack, l.lendFor(p, arg, r.mut))
				continue
			}
		}

		a := l.value(arg, want)
		if want != nil && !isBorrow(want) || k.makes() || builtin != nil && builtin.id == BuiltinReplace && i == 1 || l.isBorrowTemp(a) {
			l.take(a) // given by value, the callee or the variant owns it; a borrow made for the call ends when the call returns
		}

		l.argStack = append(l.argStack, a)
	}

	if recv != nil {
		if r, ok := recv.typ.(*Ref); ok { // the receiver is lent last, after the arguments; a borrow variable is lent again
			recvPlace = l.lendFor(recvPlace, recvExpr, r.mut)
		}
	}

	if recvExpr != nil {
		l.argStack[start] = recvPlace
	}

	for _, a := range l.argStack[start:] {
		l.given(a)
	}

	args := l.args(l.argStack[start:]...)
	l.argStack = l.argStack[:start]
	return args, recvExpr != nil
}

// keepArg records where the i-th argument arg of a call goes when it is a
// closure: to a parameter its callee may keep, which the package's end
// decides; stored, by a variant, a parameter that keeps it or a slot that
// does, kept says, a variadic any or the place replace fills; and
// spawned, into the task the call starts, when spawned says so.
func (l *lowering) keepArg(arg ast.Expr, i int, k callee, param *Var, kept, spawned bool) {
	_, variant := k.obj.(*Variant)
	p, callee := declared(k.obj, i)
	switch {
	case p != nil && p.pos != token.NoPos && mayHoldClosure(p.typ):
		l.keepFor(arg, p, callee)
		if spawned {
			l.keep(arg, "spawned", arg.Pos())
		}
	case variant, param != nil && param.kept, kept:
		l.keep(arg, "stored", arg.Pos())
	case spawned:
		l.keep(arg, "spawned", arg.Pos())
	}
}

// lendFor lends the place p, written as e, for the call: the loan ends
// when the call returns.
func (l *lowering) lendFor(p Operand, e ast.Expr, mut bool) Operand {
	t := l.lend(p, e, mut)
	l.take(t)
	return t
}

// converted evaluates the operand of a conversion: the value is taken
// when it is the same value under another name, and read, a copy made,
// between numbers and between a string and its bytes or runes.
func (l *lowering) converted(x *ast.CallExpr, arg ast.Expr) Operand {
	if target := l.typeOf(x); conversionTakes(stripRef(l.typeOf(arg)), target) {
		a := l.value(arg, target)
		l.take(a)
		return a
	}

	return l.read(arg)
}
