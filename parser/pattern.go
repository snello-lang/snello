// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import "github.com/teak-lang/teak/ast"

// patternFromExpr converts the left side of an if binding, which was
// read as an expression because the parser could not yet know it was a
// pattern, into that pattern. The forms a binding uses are a name, a
// wildcard, a tuple of them, and a variant with sub-patterns. Anything
// else is reported and becomes a wildcard so the tree stays whole. The
// full pattern grammar, for match arms, arrives with task 0.10.
//
//	x            IdentPat
//	_            WildcardPat
//	(k, v)       TuplePat{IdentPat, IdentPat}
//	Some(n)      VariantPat{Path: Some, Args: [IdentPat]}
func (p *Parser) patternFromExpr(x ast.Expr) ast.Pattern {
	switch x := x.(type) {
	case *ast.Ident:
		if x.Name == "_" {
			return &ast.WildcardPat{Underscore: x.NamePos}
		}

		return &ast.IdentPat{Name: x}
	case *ast.TupleExpr:
		pat := &ast.TuplePat{Lparen: x.Lparen, Elts: make([]ast.Pattern, len(x.Elts))}
		for i, elt := range x.Elts {
			pat.Elts[i] = p.patternFromExpr(elt)
		}

		return pat
	case *ast.CallExpr:
		switch x.Fun.(type) {
		case *ast.Ident, *ast.SelectorExpr:
			pat := &ast.VariantPat{Path: x.Fun, Args: make([]ast.Pattern, len(x.Args))}
			for i, arg := range x.Args {
				pat.Args[i] = p.patternFromExpr(arg)
			}

			return pat
		}
	}

	p.error(x.Pos(), "expected a name, a tuple or a variant pattern before ':='")
	return &ast.WildcardPat{Underscore: x.Pos()}
}
