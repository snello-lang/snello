// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/types"
)

// TypeFunc reports whether sel names a function of a type, Point.Of or
// T.Parse, which takes no receiver.
func TypeFunc(info *types.Info, sel *ast.SelectorExpr) bool {
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	return ok && fn.Static()
}

// TypeOwner returns the type d declares a function of, Point in func
// Point.Parse, or nil for any other function.
func TypeOwner(info *types.Info, d *ast.FuncDecl) *types.Named {
	if d == nil || d.Of == nil {
		return nil
	}

	tn, _ := info.Uses[d.Of.Name].(*types.TypeName)
	if tn == nil {
		return nil
	}

	n, _ := tn.Type().(*types.Named)
	return n
}
