// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// Snello has expressions C has not: match, ?, ??, if with a value, and
// &x, which allocates. Each is written as statements that leave the
// value in a temporary, before the statement that uses it. Writing
// them early must not reorder evaluation, so an operand to the left of
// one is saved in a temporary of its own first. The same holds for
// calls: Snello evaluates operands left to right, as Go does, and C
// leaves the order of arguments and initializers open.

// hasEffects reports whether evaluating x has effects whose order is
// visible, as lower.HasEffects decides.
func (e *Emitter) hasEffects(x ast.Expr) bool {
	return lower.HasEffects(e.info, x)
}

// operands writes a list of operands evaluated left to right, each
// converted to the wanted type when wants gives one. C leaves the order
// of a call's arguments and of an initializer's elements open, so an
// operand is saved in a temporary, evaluated before the ones after it,
// when its order against a later one is visible (ordered); and when a
// later operand moves a variable it reads out of, (v.Clone(), v). An
// operand whose writing wrote statements of its own, a map read saving
// its key, has them run where it stands: an earlier operand still to
// be evaluated whose order against it is visible is saved before them.
// An owned value so saved, which the operation takes, is held until
// every operand is evaluated when one after it may leave the statement,
// by ? or a return in its else, and only then moved out of its
// temporary, so that leaving drops it and taking it drops nothing
// twice.
func (e *Emitter) operands(xs []ast.Expr, wants []types.Type) []string {
	leaves := -1
	for i, x := range xs {
		if lower.MayLeave(x) {
			leaves = i
		}
	}

	parts := make([]string, len(xs))
	var savedSmall [8]bool
	saved := savedSmall[:0]
	if len(xs) <= len(savedSmall) {
		saved = savedSmall[:len(xs)]
	} else {
		saved = make([]bool, len(xs))
	}

	var kept []int // the owned values held until the last operand that may leave
	save := func(k int) {
		x := xs[k]
		var want types.Type
		if wants != nil {
			want = wants[k]
		}

		t := want
		if t == nil {
			t = e.typeOf(x)
		}

		tmp := e.temp()
		e.line(e.cType(t, x.Pos()) + " " + tmp + " = " + parts[k] + ";")
		parts[k], saved[k] = tmp, true
		switch {
		case e.lent(x, want):
			e.hold(tmp, e.typeOf(borrowed(x))) // built here and only lent: dropped when the statement ends
		case k < leaves && want != nil && !isBorrowType(want) && needsDrop(t):
			e.hold(tmp, t)
			kept = append(kept, k)
		}
	}

	for i, x := range xs {
		var want types.Type
		if wants != nil {
			want = wants[i]
		}

		mark := e.body.Len()
		parts[i] = e.valueTo(x, want)
		if e.body.Len() > mark { // x wrote statements: an earlier operand ordered against it goes before them
			e.saveBefore(mark, func() {
				for k := range i {
					if !saved[k] && !lower.IsConstant(e.info, xs[k]) && lower.Ordered(e.info, xs[k], x) {
						save(k)
					}
				}
			})
		}

		if !saved[i] && (lower.OrderedBefore(e.info, x, xs[i+1:]) || e.lent(x, want) || e.readsMoved(x, xs[i+1:])) {
			save(i)
		}
	}

	for _, i := range kept {
		parts[i] = e.taken(parts[i], wants[i], parts[i], wants[i], xs[i].Pos()) // every operand is in: the operation takes it, and the temporary is emptied
	}

	return parts
}

// saveBefore writes, with write, statements that must run before the
// ones written since the body's length was mark: those are taken out,
// write writes, and they are put back after.
func (e *Emitter) saveBefore(mark int, write func()) {
	tail := append(e.scratch[:0], e.body.Bytes()[mark:]...)
	e.body.Truncate(mark)
	write()
	e.body.Write(tail)
	e.scratch = tail[:0]
}

// readsMoved reports whether x reads a variable one of the operands
// after it moves away, anywhere inside them, (len(v), Box{s: v}): the
// move is written before the expression that holds x is, and empties
// the variable first.
func (e *Emitter) readsMoved(x ast.Expr, after []ast.Expr) bool {
	for _, y := range after {
		if e.movesRead(y, x) {
			return true
		}
	}

	return false
}

// movesRead reports whether y, or an expression inside it outside a
// function literal, moves a place whose variable x may read; an
// expression it does not look into is taken to.
func (e *Emitter) movesRead(y, x ast.Expr) bool {
	if y == nil { // a slice's bound left out
		return false
	}

	if e.info.Moves[y] {
		if v := lower.PlaceRoot(e.info, y); v != nil && lower.Mentions(e.info, x, v) {
			return true
		}
	}

	switch y := y.(type) {
	case *ast.Ident, *ast.BasicLit, *ast.TypeExpr, *ast.FuncLit: // a literal's body runs when it is called, not here
		return false
	case *ast.ParenExpr:
		return e.movesRead(y.X, x)
	case *ast.TryExpr:
		return e.movesRead(y.X, x)
	case *ast.SliceExpr:
		return e.movesRead(y.X, x) || e.movesRead(y.Low, x) || e.movesRead(y.High, x)
	case *ast.SelectorExpr:
		return e.movesRead(y.X, x)
	case *ast.IndexExpr:
		return e.movesRead(y.X, x) || e.movesRead(y.Index, x)
	case *ast.UnaryExpr:
		return e.movesRead(y.X, x)
	case *ast.BinaryExpr:
		return e.movesRead(y.X, x) || e.movesRead(y.Y, x)
	case *ast.CallExpr:
		return e.anyMovesRead(y.Args, x)
	case *ast.SpawnExpr:
		return e.movesRead(y.Call, x)
	case *ast.TupleExpr:
		return e.anyMovesRead(y.Elts, x)
	case *ast.CompositeLit:
		return lower.AnyElement(y, func(z ast.Expr) bool { return e.movesRead(z, x) })
	}

	return true
}

// anyMovesRead is movesRead over several expressions.
func (e *Emitter) anyMovesRead(ys []ast.Expr, x ast.Expr) bool {
	for _, y := range ys {
		if e.movesRead(y, x) {
			return true
		}
	}

	return false
}

// isBorrowType reports whether t is a borrow, which an operation
// lends rather than takes.
func isBorrowType(t types.Type) bool {
	_, ok := t.(*types.Ref)
	return ok
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

	if e.isLent(x) {
		return false
	}

	t := e.typeOf(x)
	if want != nil && e.builtPointer(x, t, want) {
		return false // held whole by valueTo, which reads through it
	}

	return needsDrop(t)
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
		// An element is a place whatever its index and whatever its
		// collection: index saves an index that has effects, and the
		// collection is a place or the temporary scrutinee holds for the
		// statement, so the element is named once and reached where it
		// lies. A copy here instead would be an owned second value of
		// the element, dropped while the collection still holds the
		// first.
		return true
	case *ast.UnaryExpr:
		return x.Op == token.MUL
	}

	return false
}

// try writes x?: the value inside, after a return of the absent or
// failed case as the function's own result, marked unlikely.
func (e *Emitter) try(x *ast.TryExpr) string {
	place, t, held := e.consumed(x.X)
	switch u := t.Underlying().(type) {
	case *types.Option:
		e.line("if (SN_UNLIKELY(!" + place + ".has)) {")
		e.leaveFunction(x.Pos())
		e.line("return " + e.variantValue(e.result, types.NoneVariant, nil, x.Pos()) + ";")
	case *types.Result:
		e.line("if (SN_UNLIKELY(!" + place + ".ok)) {")
		err := e.passed(x, e.taken(place+".err", u.Err(), held, e.typeOf(x.X), x.Pos()), u.Err())
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

// ifExprInto writes the branches of if c { a } else { b } into result.
// The condition's temporaries are dropped once it is decided, before
// either branch runs, as an if statement's are: an else if's are made
// and dropped inside the else's block, where its condition is written.
func (e *Emitter) ifExprInto(x *ast.IfExpr, result string, rt types.Type) {
	e.line("if " + parenthesized(e.boolean(x.Cond)) + " {")
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
