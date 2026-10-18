// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// exprEnd is the set of tokens that end an expression when they appear
// outside any bracket the expression opened.
var exprEnd = newKindSet(token.COMMA, token.SEMICOLON, token.RPAREN, token.RBRACK, token.RBRACE)

// parseExpr parses an expression. Until task 0.9 it skips the
// expression's tokens instead, balanced across brackets, up to the
// comma, semicolon or closing bracket that ends it, and returns a
// BadExpr covering them. That is enough for a declaration with an
// initializer to parse as a whole.
//
//	var n = 1 << 10          BadExpr over "1 << 10"
//	var f = func() { x; y }  BadExpr over the whole literal: the
//	                         semicolon is inside the braces
func (p *Parser) parseExpr() ast.Expr {
	bad := &ast.BadExpr{From: p.pos}
	depth := 0
	for p.tok != token.EOF && !(depth == 0 && exprEnd[p.tok]) {
		switch p.tok {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		}

		p.next()
	}

	bad.To = p.pos
	return bad
}
