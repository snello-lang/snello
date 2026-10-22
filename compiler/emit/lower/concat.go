// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// Addends returns the operands of a chain of string additions, a + b +
// c, in the order they are written and evaluated, parentheses looked
// through, for a backend to concatenate in one allocation, as Go's
// concatstrings does, rather than one a +; nil when x is not a string
// addition.
func Addends(x *ast.BinaryExpr, typeOf func(ast.Expr) types.Type) []ast.Expr {
	if !isStringAdd(x, typeOf) {
		return nil
	}

	var parts []ast.Expr
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		for {
			p, ok := e.(*ast.ParenExpr)
			if !ok {
				break
			}

			e = p.X
		}

		if b, ok := e.(*ast.BinaryExpr); ok && isStringAdd(b, typeOf) {
			walk(b.X)
			walk(b.Y)
			return
		}

		parts = append(parts, e)
	}

	walk(x)
	return parts
}

func isStringAdd(x *ast.BinaryExpr, typeOf func(ast.Expr) types.Type) bool {
	if x.Op != token.ADD {
		return false
	}

	b, ok := ValueType(typeOf(x.X)).(*types.Basic) // string itself: a named type's + may be a method of its own
	return ok && b.Kind() == types.String
}
