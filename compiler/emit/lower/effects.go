// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// HasEffects reports whether evaluating x may change what another
// operand reads, or read what another changes, so that its order among
// its neighbors is visible: a call, len and cap aside, which only read,
// but of a channel, whose length its other holders change;
// a match, an if, a ? or a ??, whose arms may call; a channel receive,
// which takes a value out; and a mut borrow of m[k] or of a part of it,
// which makes the entry m[k] when k is new.
func HasEffects(info *types.Info, x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok {
			if b, ok := info.Uses[id].(*types.Builtin); ok && (b.ID() == types.BuiltinLen || b.ID() == types.BuiltinCap) {
				return len(x.Args) == 1 && isChannel(info, x.Args[0]) || anyEffects(info, x.Args) // a channel's length is shared, and changes as other holders send and receive
			}
		}

		return true
	case *ast.MatchExpr, *ast.IfExpr, *ast.TryExpr, *ast.CoalesceExpr, *ast.SpawnExpr:
		return true
	case *ast.ParenExpr:
		return HasEffects(info, x.X)
	case *ast.TupleExpr:
		return anyEffects(info, x.Elts)
	case *ast.SelectorExpr:
		return HasEffects(info, x.X)
	case *ast.IndexExpr:
		return HasEffects(info, x.X) || HasEffects(info, x.Index)
	case *ast.SliceExpr:
		return HasEffects(info, x.X) || x.Low != nil && HasEffects(info, x.Low) || x.High != nil && HasEffects(info, x.High)
	case *ast.UnaryExpr:
		return x.Op == token.ARROW || x.Op == token.MUT && ThroughMap(info, x.X) || HasEffects(info, x.X)
	case *ast.BinaryExpr:
		return HasEffects(info, x.X) || HasEffects(info, x.Y)
	case *ast.CompositeLit:
		return AnyElement(x, func(y ast.Expr) bool { return HasEffects(info, y) })
	}

	return false
}

// isMutBorrow reports whether x is a mut borrow, a parameter or field
// of type mut T, which lends the right to change what it points at to a
// call it is passed to.
func isMutBorrow(info *types.Info, x ast.Expr) bool {
	r, ok := info.Types[x].(*types.Ref)
	return ok && r.Mut()
}

// carriesMut reports whether a mut borrow is reached through the value
// x, beyond x being one: a part of x holds one, or of what x borrows.
// What it points to is not known here.
func carriesMut(info *types.Info, x ast.Expr) bool {
	t := info.Types[x]
	if r, ok := t.(*types.Ref); ok {
		t = r.Elem()
	}

	return types.HoldsMutBorrow(t)
}

// isFunction reports whether x is a function value, which a call it is
// passed to may call.
func isFunction(info *types.Info, x ast.Expr) bool {
	t := info.Types[x]
	if t == nil {
		return false
	}

	_, ok := ValueType(t).Underlying().(*types.Signature)
	return ok
}

// isChannel reports whether x is a channel, a handle another holder of
// it receives through; false for an expression with no type, such as a
// type written as an argument, make(chan int).
func isChannel(info *types.Info, x ast.Expr) bool {
	t := info.Types[x]
	if t == nil {
		return false
	}

	_, isChan := ValueType(t).Underlying().(*types.Chan)
	return isChan
}

func anyEffects(info *types.Info, xs []ast.Expr) bool {
	for _, x := range xs {
		if HasEffects(info, x) {
			return true
		}
	}

	return false
}

// AnyElement reports whether f holds for an expression a composite
// literal's elements are written from: a map's keys and values, and the
// values of the fields a struct names, whose names are no expressions
// to evaluate and which f finds nothing in.
func AnyElement(x *ast.CompositeLit, f func(ast.Expr) bool) bool {
	for _, elt := range x.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if f(kv.Key) || f(kv.Value) {
				return true
			}

			continue
		}

		if f(elt) {
			return true
		}
	}

	return false
}

// IsConstant reports whether x is a literal or a constant, this
// package's or another's, whose value no operand changes and whose
// order is never visible.
func IsConstant(info *types.Info, x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return IsConstant(info, x.X)
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		_, ok := info.Uses[x].(*types.Const)
		return ok
	case *ast.SelectorExpr:
		_, ok := info.Uses[x.Sel].(*types.Const)
		return ok
	}

	return false
}

// PlaceRoot returns the variable at the root of the place x, v of v,
// v.f, v[i] and *v, or nil.
func PlaceRoot(info *types.Info, x ast.Expr) types.Object {
	for {
		switch y := x.(type) {
		case *ast.Ident:
			return info.Uses[y]
		case *ast.ParenExpr:
			x = y.X
		case *ast.SelectorExpr:
			x = y.X
		case *ast.IndexExpr:
			x = y.X
		case *ast.UnaryExpr:
			x = y.X
		default:
			return nil
		}
	}
}

// Mentions reports whether x may read the variable v: it names v, calls
// a function value, which may read anything its closure captured, or is
// an expression whose parts it does not look into, which may.
func Mentions(info *types.Info, x ast.Expr, v types.Object) bool {
	switch x := x.(type) {
	case nil: // a slice's bound left out
		return false
	case *ast.Ident:
		return info.Uses[x] == v
	case *ast.BasicLit, *ast.TypeExpr:
		return false
	case *ast.ParenExpr:
		return Mentions(info, x.X, v)
	case *ast.TryExpr:
		return Mentions(info, x.X, v)
	case *ast.SliceExpr:
		return Mentions(info, x.X, v) || Mentions(info, x.Low, v) || Mentions(info, x.High, v)
	case *ast.SelectorExpr:
		return Mentions(info, x.X, v)
	case *ast.IndexExpr:
		return Mentions(info, x.X, v) || Mentions(info, x.Index, v)
	case *ast.UnaryExpr:
		return Mentions(info, x.X, v)
	case *ast.BinaryExpr:
		return Mentions(info, x.X, v) || Mentions(info, x.Y, v)
	case *ast.TupleExpr:
		for _, elt := range x.Elts {
			if Mentions(info, elt, v) {
				return true
			}
		}

		return false
	case *ast.CompositeLit:
		return AnyElement(x, func(y ast.Expr) bool { return Mentions(info, y, v) })
	case *ast.SpawnExpr:
		return Mentions(info, x.Call, v)
	case *ast.CallExpr:
		if callsValue(info, x) || Mentions(info, x.Fun, v) {
			return true
		}

		for _, a := range x.Args {
			if Mentions(info, a, v) {
				return true
			}
		}

		return false
	}

	return true
}

// MayChange reports whether evaluating x may change what y reads: x
// changes a variable y mentions, or x changes what it cannot say, by
// calling a function value, and y reads anything at all.
func MayChange(info *types.Info, x, y ast.Expr) bool {
	changes := false
	changedRoots(info, x, func(v types.Object) bool {
		changes = v == nil && !IsConstant(info, y) || v != nil && Mentions(info, y, v)
		return !changes
	})

	return changes
}

// OldReadFirst reports whether the assignment lhs tok rhs reads its
// place's old value before it evaluates rhs: a compound assignment
// whose value may change what the place reads, c -= tick(mut c). Such
// an assignment reaches its place again to store, after the value.
func OldReadFirst(info *types.Info, lhs, rhs ast.Expr, tok token.Kind) bool {
	return tok != token.ASSIGN && tok != token.DEFINE && MayChange(info, rhs, lhs)
}

// changedRoots calls f with the variable at the root of each place
// evaluating x may change: the operand of mut, the receiver a method
// takes mut, a mut borrow passed on to a call, a channel received from
// or handed to a call, which may receive from it; and with nil when x
// changes what it cannot name, by calling a function value, handing
// one to a call, which may call it, or evaluating a match, an if, a ?
// or a ??, whose arms it does not look into. f returns false to stop the walk,
// and changedRoots reports whether it ran to the end.
func changedRoots(info *types.Info, x ast.Expr, f func(types.Object) bool) bool {
	switch x := x.(type) {
	case nil, *ast.Ident, *ast.BasicLit, *ast.TypeExpr, *ast.FuncLit:
		return true
	case *ast.MatchExpr, *ast.IfExpr, *ast.TryExpr, *ast.CoalesceExpr:
		return f(nil)
	case *ast.SpawnExpr:
		return changedRoots(info, x.Call, f)
	case *ast.ParenExpr:
		return changedRoots(info, x.X, f)
	case *ast.SelectorExpr:
		return changedRoots(info, x.X, f)
	case *ast.IndexExpr:
		return changedRoots(info, x.X, f) && changedRoots(info, x.Index, f)
	case *ast.SliceExpr:
		return changedRoots(info, x.X, f) && changedRoots(info, x.Low, f) && changedRoots(info, x.High, f)
	case *ast.BinaryExpr:
		return changedRoots(info, x.X, f) && changedRoots(info, x.Y, f)
	case *ast.TupleExpr:
		for _, elt := range x.Elts {
			if !changedRoots(info, elt, f) {
				return false
			}
		}

		return true
	case *ast.CompositeLit:
		return !AnyElement(x, func(y ast.Expr) bool { return !changedRoots(info, y, f) })
	case *ast.UnaryExpr:
		if x.Op == token.MUT || x.Op == token.ARROW {
			if !f(PlaceRoot(info, x.X)) {
				return false
			}
		}

		return changedRoots(info, x.X, f)
	case *ast.CallExpr:
		if callsValue(info, x) && !f(nil) {
			return false
		}

		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if takesMut(info, sel) && !f(PlaceRoot(info, sel.X)) {
				return false
			}

			if carriesMut(info, sel.X) && !f(nil) { // a receiver holding a mut borrow, w.Bump() with w := W{p: r}: the method may change what it points to
				return false
			}
		}

		for _, a := range x.Args {
			if (isChannel(info, a) || isMutBorrow(info, a)) && !f(PlaceRoot(info, a)) { // a handle or a mut borrow passed on: the callee may change what it reaches
				return false
			}

			if carriesMut(info, a) && !f(nil) { // a value holding a mut borrow, W{p: r} or an option of one: what it points to may change
				return false
			}

			if isFunction(info, a) && !f(nil) { // a closure passed on, which the callee may call
				return false
			}

			if !changedRoots(info, a, f) {
				return false
			}
		}

		return changedRoots(info, x.Fun, f)
	}

	return f(nil)
}

// callsValue reports whether the call x calls a function value, a
// variable, a field or a call's result of a function type, rather than
// a function or a method the program declares.
func callsValue(info *types.Info, x *ast.CallExpr) bool {
	switch fun := x.Fun.(type) {
	case *ast.Ident:
		_, value := info.Uses[fun].(*types.Var)
		return value
	case *ast.SelectorExpr:
		_, value := info.Uses[fun.Sel].(*types.Var)
		return value
	case *ast.FuncLit:
		return true
	case *ast.ParenExpr, *ast.CallExpr, *ast.IndexExpr:
		return true
	}

	return false
}

// takesMut reports whether sel names a method whose receiver is mut, as
// xs.Push(v) changes xs.
func takesMut(info *types.Info, sel *ast.SelectorExpr) bool {
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return false
	}

	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}

	r, ok := sig.Recv().Type().(*types.Ref)
	return ok && r.Mut()
}

// Ordered reports whether the order of the operands a, then b, is
// visible: both have effects, which happen in the order written, or one
// may change what the other reads.
func Ordered(info *types.Info, a, b ast.Expr) bool {
	return HasEffects(info, a) && HasEffects(info, b) || MayChange(info, a, b) || MayChange(info, b, a)
}

// OrderedBefore reports whether x is ordered against one of the
// operands after it.
func OrderedBefore(info *types.Info, x ast.Expr, after []ast.Expr) bool {
	for _, y := range after {
		if Ordered(info, x, y) {
			return true
		}
	}

	return false
}
