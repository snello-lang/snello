// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"

	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/types"
)

// deferStmt writes defer f(args): the arguments, and a callee that is
// a value, are evaluated now into variables declared at the top of the
// function, and a flag records that this defer ran. The call itself is
// written once, as the lines every return of the function runs when
// the flag is set, last deferred first. A receiver lent mutably is
// not saved: the call must change the receiver itself.
func (e *Emitter) deferStmt(s *ast.DeferStmt) {
	call, ok := s.Call.(*ast.CallExpr)
	if !ok {
		e.unsupported(s.Pos(), "defer of something that is not a call")
		return
	}

	if len(e.loops) > 0 {
		e.unsupported(s.Pos(), "defer inside a loop")
		return
	}

	for _, a := range call.Args {
		e.pre[a] = e.atTop(a, e.typeOf(a))
	}

	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr: // a method: its receiver, unless lent mutably
		if e.packageOf(fun.X) == nil && !e.mutReceiver(fun) {
			e.pre[fun.X] = e.atTop(fun.X, e.typeOf(fun.X))
		}
	case *ast.Ident: // a function value in a variable; a named function needs nothing
		if _, variable := e.info.Uses[fun].(*types.Var); variable {
			e.pre[fun] = e.atTop(fun, e.typeOf(fun))
		}
	default:
		e.pre[fun] = e.atTop(fun, e.typeOf(fun))
	}

	flag := e.temp() + "_deferred"
	e.prologue = append(e.prologue, "bool "+flag+" = false;")
	e.line(flag + " = true;")

	// The call's lines go into a buffer of their own, to be written at
	// each return.
	outer := e.body
	e.body = bytes.Buffer{}
	e.scoped(call.Pos(), func() { // the call's own temporaries are dropped with it
		if v := e.expr(call); v != "" {
			e.discard(v, e.typeOf(call), call.Pos())
		}
	})

	code := e.body.String()
	e.body = outer
	e.defers = append(e.defers, deferred{flag, code})
}

// runDefers writes the deferred calls of the function, last first,
// each under its flag.
func (e *Emitter) runDefers() {
	for i := len(e.defers) - 1; i >= 0; i-- {
		e.line("if (" + e.defers[i].flag + ") {")
		e.body.WriteString(e.defers[i].code)
		e.line("}")
	}
}

// atTop evaluates x into a variable declared at the top of the
// function, so that it is in scope at every return, and returns the
// variable's name.
func (e *Emitter) atTop(x ast.Expr, t types.Type) string {
	v := e.temp()
	e.prologue = append(e.prologue, e.cType(t, x.Pos())+" "+v+" = "+zeroValue+";")
	e.line(v + " = " + e.expr(x) + ";")
	return v
}

// mutReceiver reports whether a method call lends its receiver
// mutably.
func (e *Emitter) mutReceiver(sel *ast.SelectorExpr) bool {
	fn, ok := e.info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return mutating[sel.Sel.Name] // a predeclared method
	}

	r, ok := fn.Type().(*types.Signature).Recv().Type().(*types.Ref)
	return ok && r.Mut()
}
