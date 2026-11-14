// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"testing"

	"github.com/snello-lang/snello/compiler/ast"
)

// TestTypeFunc checks what the backends ask of a function of a type:
// the type a declaration belongs to, and whether a call's selector names
// one rather than a method.
func TestTypeFunc(t *testing.T) {
	f, info := checkSource(t, `package main
struct P { x int }
func P.Of(x int) P { return P{x: x} }
func (p ref P) X() int { return p.x }
func plain() int { return 1 }
func main() { p := P.Of(1); print(p.X(), plain()) }`)
	of, method, plain := f.Decls[1].(*ast.FuncDecl), f.Decls[2].(*ast.FuncDecl), f.Decls[3].(*ast.FuncDecl)
	if n := TypeOwner(info, of); n == nil || n.Obj().Name() != "P" {
		t.Errorf("P.Of: owner %v, want P", n)
	}

	if TypeOwner(info, method) != nil || TypeOwner(info, plain) != nil {
		t.Errorf("a method or a plain function has an owner, want none")
	}

	body := f.Decls[4].(*ast.FuncDecl).Body.List
	ofCall := body[0].(*ast.AssignStmt).Rhs[0].(*ast.CallExpr).Fun.(*ast.SelectorExpr)
	xCall := body[1].(*ast.ExprStmt).X.(*ast.CallExpr).Args[0].(*ast.CallExpr).Fun.(*ast.SelectorExpr)
	if !TypeFunc(info, ofCall) || TypeFunc(info, xCall) {
		t.Errorf("TypeFunc: P.Of %v, p.X %v; want true, false", TypeFunc(info, ofCall), TypeFunc(info, xCall))
	}
}
