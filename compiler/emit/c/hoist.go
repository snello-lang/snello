// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// Teak has expressions C has not: match, ?, ??, if with a value, and
// &x, which allocates. Each is written as statements that leave the
// value in a temporary, before the statement that uses it. Writing
// them early must not reorder evaluation, so an operand to the left of
// one is saved in a temporary of its own first. The same holds for
// calls: Teak evaluates operands left to right, as Go does, and C
// leaves the order of arguments and initializers open.

// needsHoist reports whether writing x produces statements.
func needsHoist(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.MatchExpr, *ast.TryExpr, *ast.CoalesceExpr, *ast.IfExpr:
		return true
	case *ast.ParenExpr:
		return needsHoist(x.X)
	case *ast.TupleExpr:
		return anyHoist(x.Elts)
	case *ast.SelectorExpr:
		return needsHoist(x.X)
	case *ast.IndexExpr:
		return needsHoist(x.X) || needsHoist(x.Index)
	case *ast.CallExpr:
		return needsHoist(x.Fun) || anyHoist(x.Args)
	case *ast.UnaryExpr:
		return x.Op == token.AND || needsHoist(x.X)
	case *ast.BinaryExpr:
		return needsHoist(x.X) || needsHoist(x.Y)
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if needsHoist(elt) {
				return true
			}
		}
	}

	return false
}

// hasEffects reports whether evaluating x may call a function or
// needs statements, so that its order among its neighbors is visible.
// len and cap only read, so they count as what their argument is.
func (e *Emitter) hasEffects(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok {
			if b, ok := e.info.Uses[id].(*types.Builtin); ok && (b.ID() == types.BuiltinLen || b.ID() == types.BuiltinCap) {
				return e.anyEffects(x.Args)
			}
		}

		return true
	case *ast.ParenExpr:
		return e.hasEffects(x.X)
	case *ast.TupleExpr:
		return e.anyEffects(x.Elts)
	case *ast.SelectorExpr:
		return e.hasEffects(x.X)
	case *ast.IndexExpr:
		return e.hasEffects(x.X) || e.hasEffects(x.Index)
	case *ast.UnaryExpr:
		return e.hasEffects(x.X)
	case *ast.BinaryExpr:
		return e.hasEffects(x.X) || e.hasEffects(x.Y)
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if e.hasEffects(elt) {
				return true
			}
		}
	}

	return needsHoist(x)
}

func (e *Emitter) anyEffects(xs []ast.Expr) bool {
	for _, x := range xs {
		if e.hasEffects(x) {
			return true
		}
	}

	return false
}

func anyHoist(xs []ast.Expr) bool {
	for _, x := range xs {
		if needsHoist(x) {
			return true
		}
	}

	return false
}

// operands writes a list of operands evaluated left to right, each
// converted to the wanted type when wants gives one. Every operand to
// the left of the last one that calls or needs statements is saved in
// a temporary, so it is evaluated before that one.
func (e *Emitter) operands(xs []ast.Expr, wants []types.Type) []string {
	last := -1
	for i, x := range xs {
		if e.hasEffects(x) {
			last = i
		}
	}

	parts := make([]string, len(xs))
	for i, x := range xs {
		var want types.Type
		if wants != nil {
			want = wants[i]
		}

		parts[i] = e.valueTo(x, want)
		t := want
		if t == nil {
			t = e.typeOf(x)
		}

		if i < last || e.lent(x, want) {
			tmp := e.temp()
			e.line(e.cType(t, x.Pos()) + " " + tmp + " = " + parts[i] + ";")
			parts[i] = tmp
			if e.lent(x, want) {
				e.hold(tmp, e.typeOf(borrowed(x))) // built here and only lent: dropped when the statement ends
			}
		}
	}

	return parts
}

// lent reports whether x is a value built here that the operation
// only looks at, as a ref parameter or a print does, so that nothing
// else will drop it.
func (e *Emitter) lent(x ast.Expr, want types.Type) bool {
	if u, ok := x.(*ast.UnaryExpr); ok && (u.Op == token.REF || u.Op == token.MUT) {
		return false // a borrow written out holds what it lends itself (unary)
	}

	if want != nil {
		r, ok := want.(*types.Ref)
		if !ok || r.Mut() {
			return false
		}
	}

	x = borrowed(x)
	if _, literal := x.(*ast.BasicLit); literal {
		return false // a literal owns nothing
	}

	return !e.isRead(x) && needsDrop(e.typeOf(x))
}

// borrowed returns the value an operand lends: the operand of a ref,
// which may be built there, else the operand itself.
func borrowed(x ast.Expr) ast.Expr {
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.REF {
		return u.X
	}

	return x
}

// evaluated writes operands that are consumed one at a time, as a
// print consumes its arguments: every one with an effect is saved, so
// all are evaluated before the first is used.
func (e *Emitter) evaluated(xs []ast.Expr) []string {
	parts := e.operands(xs, nil)
	if n := len(xs); n > 0 && e.hasEffects(xs[n-1]) {
		tmp := e.temp()
		e.line(e.cType(e.typeOf(xs[n-1]), xs[n-1].Pos()) + " " + tmp + " = " + parts[n-1] + ";")
		parts[n-1] = tmp
	}

	return parts
}

// scrutinee writes the value a match, ?, ?? or if-binding looks into,
// as a place its parts can be read from and bound to: the expression
// itself when it denotes a place, otherwise a temporary holding it,
// dropped when the statement ends. It returns the place and the type
// of the value there.
func (e *Emitter) scrutinee(x ast.Expr) (string, types.Type) {
	place, t, held := e.consumed(x)
	if held != "" {
		e.hold(held, e.typeOf(x))
	}

	return place, t
}

// consumed is scrutinee for an operation that takes the value apart:
// a value built here, or a place whose value moves into the operation,
// goes into a temporary the caller owns, returned as the third result
// and empty for a place read where it is. The caller moves parts out of
// the temporary, emptying each, and drops the rest.
func (e *Emitter) consumed(x ast.Expr) (string, types.Type, string) {
	t := e.typeOf(x)
	if e.isPlace(x) && !e.info.Moves[x] {
		return e.place(x), lower.ValueType(t), ""
	}

	var tmp string
	if e.isPlace(x) {
		tmp = e.moved(x)
	} else {
		tmp = e.temp()
		e.line(e.cType(t, x.Pos()) + " " + tmp + " = " + e.expr(x) + ";")
	}

	e.line("(void)" + tmp + ";")
	return deref(tmp, t), lower.ValueType(t), tmp
}

// changed writes a place a method is about to change, as scrutinee
// does, except that a map element is made when its key is new.
func (e *Emitter) changed(x ast.Expr) (string, types.Type) {
	if t := e.typeOf(x); e.isPlace(x) {
		return deref(e.lvalue(x), t), lower.ValueType(t)
	}

	return e.scrutinee(x)
}

// isPlace reports whether x denotes a variable or a part of a value,
// which can be read in place and bound to without a copy. A field, an
// element or what a pointer points at is a place wherever the value
// is: in a variable, behind a borrow, or in the temporary scrutinee
// holds for a value built here, which the statement drops whole, so
// that its part is never copied out and dropped twice.
func (e *Emitter) isPlace(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		_, ok := e.info.Uses[x].(*types.Var)
		return ok
	case *ast.ParenExpr:
		return e.isPlace(x.X)
	case *ast.SelectorExpr:
		v, ok := e.info.Uses[x.Sel].(*types.Var)
		if ok && e.packageOf(x.X) != nil {
			return true // a variable of the library, such as os.Args
		}

		return ok && v.IsField()
	case *ast.IndexExpr:
		return !e.hasEffects(x.Index)
	case *ast.UnaryExpr:
		return x.Op == token.MUL
	}

	return false
}

// try writes x?: the value inside, after a return of the absent or
// failed case as the function's own result.
func (e *Emitter) try(x *ast.TryExpr) string {
	place, t, held := e.consumed(x.X)
	switch u := t.Underlying().(type) {
	case *types.Option:
		e.line("if (!" + place + ".has) {")
		e.leaveFunction(x.Pos())
		e.line("return " + e.variantValue(e.result, types.NoneVariant, nil, x.Pos()) + ";")
	case *types.Result:
		e.line("if (!" + place + ".ok) {")
		err := e.taken(place+".err", u.Err(), held, e.typeOf(x.X), x.Pos())
		e.leaveFunction(x.Pos())
		e.line("return " + e.variantValue(e.result, types.ErrVariant, []string{err}, x.Pos()) + ";")
	}

	e.line("}")
	return e.taken(place+".val", e.typeOf(x), held, e.typeOf(x.X), x.Pos())
}

// taken writes the move of the value at place, of type t, out of the
// consumed scrutinee held, of type heldType: into a temporary, with
// the scrutinee emptied so that its drop releases nothing twice. When
// nothing was consumed, held is empty and the value is read in place.
func (e *Emitter) taken(place string, t types.Type, held string, heldType types.Type, pos token.Pos) string {
	if held == "" {
		return place
	}

	v := e.temp()
	e.line(e.cType(t, pos) + " " + v + " = " + place + ";")
	e.line(held + " = " + e.zero(heldType, pos) + ";")
	return v
}

// leaveFunction writes the drops a return from inside an expression
// runs: the deferred calls, then every open frame.
func (e *Emitter) leaveFunction(pos token.Pos) {
	e.runDefers()
	e.leave(0, pos)
}

// coalesce writes x ?? else: the value inside when there is one, or
// the else value; an else that leaves, by return, break or continue,
// is written as the statement it is. Through a mut borrow the result
// is the address of the value inside, so it can be written through.
func (e *Emitter) coalesce(x *ast.CoalesceExpr) string {
	rt := e.typeOf(x)
	place, t, held := e.consumed(x.X)
	test, val := place+".has", place+".val"
	r, isResult := t.Underlying().(*types.Result)
	if isResult {
		test = place + ".ok"
	}

	if r, ok := rt.(*types.Ref); ok && r.Mut() {
		val = "&" + val
	}

	result := e.temp()
	e.line(e.cType(rt, x.Pos()) + " " + result + ";")
	e.line("if (" + test + ") {")
	e.line(result + " = " + e.taken(val, rt, held, e.typeOf(x.X), x.Pos()) + ";")
	e.line("} else {")
	if held != "" && isResult && needsDrop(r.Err()) {
		e.line(e.dropOf(place+".err", r.Err(), x.Pos())) // the error is not looked at
		e.line(held + " = " + e.zero(e.typeOf(x.X), x.Pos()) + ";")
	}

	e.node(x.Else, result, rt)
	e.line("}")
	return result
}

// node writes what follows => in a match arm or ?? in a coalesce: an
// expression, whose value goes to result; a block, whose last
// expression does; or a statement that leaves.
func (e *Emitter) node(n ast.Node, result string, want types.Type) {
	switch n := n.(type) {
	case ast.Expr:
		e.assignOrRun(result, n, want)
	case *ast.BlockStmt:
		e.blockValue(n, result, want)
	case ast.Stmt:
		e.stmt(n)
	}
}

// ifExpr writes if c { a } else { b } with a value: both branches
// assign the same temporary.
func (e *Emitter) ifExpr(x *ast.IfExpr) string {
	rt := e.typeOf(x)
	result := ""
	if yields(rt) {
		result = e.temp()
		e.line(e.cType(rt, x.Pos()) + " " + result + ";")
	}

	e.ifExprInto(x, result, rt)
	return result
}

func (e *Emitter) ifExprInto(x *ast.IfExpr, result string, rt types.Type) {
	e.line("if " + e.cond(x.Cond) + " {")
	e.blockValue(x.Then, result, rt)
	e.line("} else {")
	switch els := x.Else.(type) {
	case *ast.BlockStmt:
		e.blockValue(els, result, rt)
	case *ast.IfExpr:
		e.ifExprInto(els, result, rt)
	}

	e.line("}")
}

// blockValue writes a block whose last expression is its value,
// assigned to result when the block yields one.
func (e *Emitter) blockValue(b *ast.BlockStmt, result string, want types.Type) {
	if len(b.List) == 0 {
		return
	}

	e.push(false)
	last := len(b.List) - 1
	e.stmts(b.List[:last])
	if es, ok := b.List[last].(*ast.ExprStmt); ok {
		e.assignOrRun(result, es.X, want)
	} else {
		e.stmt(b.List[last])
	}

	e.pop(b.Rbrace)
}

// assignOrRun writes x as the value of result, or as a statement when
// there is no result to assign, as for a branch that leaves or an
// expression whose value is ().
func (e *Emitter) assignOrRun(result string, x ast.Expr, want types.Type) {
	e.scoped(x.Pos(), func() {
		v := e.valueTo(x, want)
		switch {
		case v == "":
		case result == "" || isNever(e.typeOf(x)):
			e.discard(v, e.typeOf(x), x.Pos())
		default:
			e.line(result + " = " + v + ";")
		}
	})
}

// yields reports whether an expression of type t leaves a value to
// hold: not (), and not never, the type of a branch that leaves.
func yields(t types.Type) bool {
	return t != nil && t != types.Unit && !isNever(t)
}

func isNever(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.Never
}
