// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
	"github.com/teak-lang/teak/types"
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
func hasEffects(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.CallExpr:
		return true
	case *ast.ParenExpr:
		return hasEffects(x.X)
	case *ast.TupleExpr:
		return anyEffects(x.Elts)
	case *ast.SelectorExpr:
		return hasEffects(x.X)
	case *ast.IndexExpr:
		return hasEffects(x.X) || hasEffects(x.Index)
	case *ast.UnaryExpr:
		return hasEffects(x.X)
	case *ast.BinaryExpr:
		return hasEffects(x.X) || hasEffects(x.Y)
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}

			if hasEffects(elt) {
				return true
			}
		}
	}

	return needsHoist(x)
}

func anyEffects(xs []ast.Expr) bool {
	for _, x := range xs {
		if hasEffects(x) {
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
		if hasEffects(x) {
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
		if i < last {
			t := want
			if t == nil {
				t = e.info.Types[x]
			}

			tmp := e.temp()
			e.line(e.cType(t, x.Pos()) + " " + tmp + " = " + parts[i] + ";")
			parts[i] = tmp
		}
	}

	return parts
}

// evaluated writes operands that are consumed one at a time, as a
// print consumes its arguments: every one with an effect is saved, so
// all are evaluated before the first is used.
func (e *Emitter) evaluated(xs []ast.Expr) []string {
	parts := e.operands(xs, nil)
	if n := len(xs); n > 0 && hasEffects(xs[n-1]) {
		tmp := e.temp()
		e.line(e.cType(e.info.Types[xs[n-1]], xs[n-1].Pos()) + " " + tmp + " = " + parts[n-1] + ";")
		parts[n-1] = tmp
	}

	return parts
}

// scrutinee writes the value a match, ?, ?? or if-binding looks into,
// as a place its parts can be read from and bound to: the expression
// itself when it denotes a place, otherwise a temporary holding it.
// It returns the place and the type of the value there.
func (e *Emitter) scrutinee(x ast.Expr) (string, types.Type) {
	t := e.info.Types[x]
	if e.isPlace(x) {
		return e.place(x), valueType(t)
	}

	tmp := e.temp()
	e.line(e.cType(t, x.Pos()) + " " + tmp + " = " + e.expr(x) + ";")
	e.line("(void)" + tmp + ";")
	return deref(tmp, t), valueType(t)
}

// isPlace reports whether x denotes a variable or a field of one,
// which can be read in place and bound to without a copy.
func (e *Emitter) isPlace(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		_, ok := e.info.Uses[x].(*types.Var)
		return ok
	case *ast.ParenExpr:
		return e.isPlace(x.X)
	case *ast.SelectorExpr:
		v, ok := e.info.Uses[x.Sel].(*types.Var)
		return ok && v.IsField() && e.isPlace(x.X)
	case *ast.IndexExpr:
		return e.isPlace(x.X) && !hasEffects(x.Index)
	case *ast.UnaryExpr:
		return x.Op == token.MUL && e.isPlace(x.X)
	}

	return false
}

// try writes x?: the value inside, after a return of the absent or
// failed case as the function's own result.
func (e *Emitter) try(x *ast.TryExpr) string {
	place, t := e.scrutinee(x.X)
	switch t.Underlying().(type) {
	case *types.Option:
		e.line("if (!" + place + ".has) {")
		e.line("return " + e.variantValue(e.result, types.NoneVariant, nil, x.Pos()) + ";")
	case *types.Result:
		e.line("if (!" + place + ".ok) {")
		e.line("return " + e.variantValue(e.result, types.ErrVariant, []string{place + ".err"}, x.Pos()) + ";")
	}

	e.line("}")
	return place + ".val"
}

// coalesce writes x ?? else: the value inside when there is one, or
// the else value; an else that leaves, by return, break or continue,
// is written as the statement it is. Through a mut borrow the result
// is the address of the value inside, so it can be written through.
func (e *Emitter) coalesce(x *ast.CoalesceExpr) string {
	rt := e.info.Types[x]
	place, t := e.scrutinee(x.X)
	test, val := place+".has", place+".val"
	if _, ok := t.Underlying().(*types.Result); ok {
		test = place + ".ok"
	}

	if r, ok := rt.(*types.Ref); ok && r.Mut() {
		val = "&" + val
	}

	result := e.temp()
	e.line(e.cType(rt, x.Pos()) + " " + result + ";")
	e.line("if (" + test + ") {")
	e.line(result + " = " + val + ";")
	e.line("} else {")
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
	rt := e.info.Types[x]
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

	last := len(b.List) - 1
	e.stmts(b.List[:last])
	if es, ok := b.List[last].(*ast.ExprStmt); ok {
		e.assignOrRun(result, es.X, want)
		return
	}

	e.stmt(b.List[last])
}

// assignOrRun writes x as the value of result, or as a statement when
// there is no result to assign, as for a branch that leaves or an
// expression whose value is ().
func (e *Emitter) assignOrRun(result string, x ast.Expr, want types.Type) {
	v := e.valueTo(x, want)
	switch {
	case v == "":
	case result == "" || isNever(e.info.Types[x]):
		e.discard(v)
	default:
		e.line(result + " = " + v + ";")
	}
}

// discard writes an expression as a statement, cast to void: the C
// compiler warns about an unused value, and the text of a call may be
// a temporary's field once the call was lowered.
func (e *Emitter) discard(v string) {
	e.line("(void)(" + v + ");")
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
