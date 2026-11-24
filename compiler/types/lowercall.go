// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
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

// calleeOf resolves what the call x names.
func (l *lowering) calleeOf(x *ast.CallExpr) callee {
	var k callee
	id := calleeName(x.Fun)
	if id == nil {
		_, k.isType = unparen(x.Fun).(*ast.TypeExpr)
	} else {
		k.named, k.obj = true, l.c.info.Uses[id]
		if inst, ok := l.c.info.Instances[id]; ok {
			k.sig = inst.Type
			return k
		}
	}

	switch k.obj.(type) {
	case *Builtin, *Variant, *Part, *TypeName:
		return k
	}

	k.sig, _ = l.typeOf(x.Fun).(*Signature)
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
	k := l.calleeOf(x)
	if !k.names() {
		if p, ok := l.placeOf(x.Fun); ok { // a closure held in a variable or a field is read
			l.emit(Instr{Op: OpRead, Place: p, Expr: x.Fun, Pos: x.Fun.Pos()})
		} else {
			l.take(l.value(x.Fun, nil)) // a closure written here, or a call that gives a function: made before the arguments
		}
	}

	args := l.callArgs(x, k)
	tmp := l.temp(l.typeOf(x), true)
	op := OpCall
	if k.makes() {
		op = OpMake
	}

	l.emit(Instr{Op: op, To: tmp, Args: args, Expr: x, Pos: x.Pos()})
	return tmp
}

// callArgs evaluates what a call takes, in order, and returns the
// operands, the receiver's lend first when the callee is a method.
func (l *lowering) callArgs(x *ast.CallExpr, k callee) []Operand {
	if k.conversion() && len(x.Args) == 1 {
		return l.args(l.converted(x, x.Args[0]))
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
			l.emit(Instr{Op: OpRead, Place: p, Expr: sel.X, Pos: sel.X.Pos()}) // the receiver is read before the arguments
		} else {
			recvPlace = l.value(sel.X, recv.typ)
			l.take(recvPlace)
			recv = nil // given, not lent after the arguments
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
			l.argStack = append(l.argStack, l.read(arg))
			continue
		}

		var want Type
		if k.sig != nil && (i < k.sig.fixed() || k.sig.variadic) {
			_, want, _, _ = argSlot(k.sig, i, x.Ellipsis.IsValid(), kind, each)
		}

		if isBorrow(want) && isBorrow(l.typeOf(arg)) { // a borrow passed on is lent again, not moved
			l.argStack = append(l.argStack, l.read(arg))
			continue
		}

		a := l.value(arg, want)
		if want != nil && !isBorrow(want) || builtin != nil && builtin.id == BuiltinReplace && i == 1 {
			l.take(a) // given by value: the callee owns it
		}

		l.argStack = append(l.argStack, a)
	}

	if recv != nil {
		if r, ok := recv.typ.(*Ref); ok && !isBorrow(l.typeOf(recvExpr)) { // an owned place is lent last, after the arguments
			recvPlace = l.lend(recvPlace, recvExpr, r.mut)
		}
	}

	if recvExpr != nil {
		l.argStack[start] = recvPlace
	}

	args := l.args(l.argStack[start:]...)
	l.argStack = l.argStack[:start]
	return args
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
