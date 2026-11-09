// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"bytes"
	"slices"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// A deferred call runs on every exit of its function, a return or a
// panic. The defer statement saves what the call needs into a record,
// a local at the top of the function: the arguments, a receiver it
// takes by value, a callee that is a value, and the address of a
// receiver it lends, by ref or mut, which the call reads when it runs. The call itself is a
// function of its own, the thunk, taking the record's address. The
// statement sets the record's flag and registers the record with the
// thunk on the runtime's guard stack, as a value with a Drop method
// registers itself with its dropper, so that a panic runs the call. A
// return takes the guard off and calls the thunk, last registered
// first among the values and deferred calls of the function.

// deferStmt writes defer f(args): the record's type and the thunk into
// their sections, and here the saved values, the flag and the guard.
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

	e.closures++
	n := strconv.Itoa(e.closures)
	thunk, typ, rec := "sn_defer_"+n, "struct sn_drec_"+n, e.temp()
	e.prologue = append(e.prologue, typ+" "+rec+" = "+zeroValue+";")
	var fields strings.Builder
	var saved []ast.Expr
	save := func(x ast.Expr) {
		field := "a" + strconv.Itoa(len(saved))
		put(&fields, "\t", e.cType(e.typeOf(x), x.Pos()), " ", field, ";\n")
		e.line(rec + "." + field + " = " + e.expr(x) + ";")
		saved = append(saved, x)
	}

	for _, a := range call.Args {
		save(a)
	}

	var recv *types.Var // a receiver lent, reached through its address
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr: // a method: its receiver, by value or by address
		switch {
		case e.packageOf(fun.X) != nil:
		case !e.lentReceiver(fun):
			save(fun.X)
		default:
			recv = lower.Variable(e.info, fun.X)
			if recv == nil {
				e.unsupported(fun.X.Pos(), "a deferred call's receiver that is not a variable")
				return
			}

			put(&fields, "\t", e.cType(e.sub(recv.Type()), fun.X.Pos()), " *r;\n")
			e.line(rec + ".r = &" + e.rawPlace(fun.X) + ";")
		}
	case *ast.Ident: // a function value in a variable; a named function needs nothing
		if _, variable := e.info.Uses[fun].(*types.Var); variable {
			save(fun)
		}
	default:
		save(fun)
	}

	put(&e.types, typ, " {\n\tbool on;\n", fields.String(), "};\n\n")
	put(&e.protos, "static void ", thunk, "(void *sn_p);\n")
	e.thunk(thunk, typ, call, saved, recv)
	e.line(rec + ".on = true;")
	e.line("sn_guard(&" + rec + ", " + thunk + ");")
	e.seq++
	e.defers = append(e.defers, deferred{rec, thunk, e.seq})
}

// thunk writes the function that makes the deferred call from its
// record: the saved values read from the record's fields, a lent
// receiver through its address, and the call's own temporaries
// dropped with it. The function being written is kept.
func (e *Emitter) thunk(name, typ string, call *ast.CallExpr, saved []ast.Expr, recv *types.Var) {
	outerBody, outerPrologue, outerFrames, outerTemps, outerCaptured := e.body, e.prologue, e.frames, e.temps, e.captured
	e.body, e.prologue, e.frames, e.temps = bytes.Buffer{}, nil, nil, nil
	e.captured = make(map[*types.Var]string, 1) // the function's own variables are out of reach here, and the call names none but the receiver
	if recv != nil {
		e.captured[recv] = "(*sn_d->r)"
	}

	for i, x := range saved {
		e.pre[x] = "sn_d->a" + strconv.Itoa(i)
	}

	e.scoped(call.Pos(), func() {
		if v := e.expr(call); v != "" {
			e.discard(v, e.typeOf(call), call.Pos())
		}
	})

	for _, x := range saved {
		delete(e.pre, x)
	}

	put(&e.helpers, "static void ", name, "(void *sn_p) {\n", typ, " *sn_d = sn_p;\n(void)sn_d;\n")
	if len(e.prologue) > 0 {
		put(&e.helpers, strings.Join(e.prologue, "\n"), "\n")
	}

	e.helpers.Write(e.body.Bytes())
	e.helpers.WriteString("}\n\n")
	e.body, e.prologue, e.frames, e.temps, e.captured = outerBody, outerPrologue, outerFrames, outerTemps, outerCaptured
}

// runDefer writes a deferred call at a return: when its statement ran,
// its guard comes off, so that a panic in the call does not run it
// again, and the thunk makes the call.
func (e *Emitter) runDefer(d deferred) {
	e.line("if (" + d.rec + ".on) {")
	e.line("sn_unguard(&" + d.rec + ");")
	e.line(d.thunk + "(&" + d.rec + ");")
	e.line("}")
}

// leaveFunction writes what a return runs: the temporaries pending,
// the variables of every open frame, innermost first, and the deferred
// calls that ran, each before every value registered before it, so
// that a deferred call runs before the drops of what it may borrow, as
// a panic's unwinding of the guard stack runs it.
func (e *Emitter) leaveFunction(pos token.Pos) {
	var order []owned
	for _, v := range slices.Backward(e.temps[e.frames[0].mark:]) {
		order = append(order, v)
	}

	for _, f := range slices.Backward(e.frames) {
		for _, v := range slices.Backward(f.vars) {
			order = append(order, v)
		}
	}

	d := len(e.defers)
	for _, v := range order {
		for ; d > 0 && e.defers[d-1].seq > v.seq; d-- {
			e.runDefer(e.defers[d-1])
		}

		e.dropVars([]owned{v}, pos)
	}

	for ; d > 0; d-- {
		e.runDefer(e.defers[d-1])
	}
}

// lentReceiver reports whether a method call lends its receiver, by
// ref or mut, so that the deferred call reads the variable when it
// runs, rather than taking it by value, as Option's and Result's
// Expect, Unwrap, UnwrapOr, Map and MapErr do; every other predeclared
// method lends its receiver.
func (e *Emitter) lentReceiver(sel *ast.SelectorExpr) bool {
	fn, ok := e.info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return !consuming[sel.Sel.Name]
	}

	_, ok = fn.Type().(*types.Signature).Recv().Type().(*types.Ref)
	return ok
}

// The predeclared methods that take their receiver by value.
var consuming = map[string]bool{"Expect": true, "Unwrap": true, "UnwrapOr": true, "Map": true, "MapErr": true}
