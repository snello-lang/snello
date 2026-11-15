// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/types"
)

// PlaceOperands calls f, in the order they are evaluated, for each
// operand of the place x an assignment writes to that is a value of its
// own: the index or key of each element on the way, a[i][k] giving i,
// then k. The collections and fields on the way are places, reached
// where they are.
func PlaceOperands(x ast.Expr, f func(ast.Expr)) {
	switch x := x.(type) {
	case *ast.ParenExpr:
		PlaceOperands(x.X, f)
	case *ast.SelectorExpr:
		PlaceOperands(x.X, f)
	case *ast.UnaryExpr:
		PlaceOperands(x.X, f)
	case *ast.IndexExpr:
		PlaceOperands(x.X, f)
		f(x.Index)
	}
}

// ThroughMap reports whether the place x an assignment writes to is, or
// lies inside, an entry of a map, m[k] or m[k].f, which the assignment
// makes when k is new: the value assigned is evaluated before that, so
// that it does not see the entry.
func ThroughMap(info *types.Info, x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return ThroughMap(info, x.X)
	case *ast.SelectorExpr:
		return ThroughMap(info, x.X)
	case *ast.UnaryExpr:
		return ThroughMap(info, x.X)
	case *ast.IndexExpr:
		if t := info.Types[x.X]; t != nil {
			if _, isMap := ValueType(t).Underlying().(*types.Map); isMap {
				return true
			}
		}

		return ThroughMap(info, x.X)
	}

	return false
}
