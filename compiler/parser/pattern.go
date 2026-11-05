// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// parsePattern parses a pattern of a match arm. The grammar:
//
//	Pattern = Single {"|" Single}
//	Single  = "_" | ["-"] literal ["..=" ["-"] literal]
//	        | Path ["(" [Pattern {"," Pattern}] ")"]
//	        | "(" [Pattern {"," Pattern}] ")"
//	Path    = ident {"." ident}
//
// A bare name is an IdentPat whether it binds a variable or names a
// constant variant such as None; the checker decides, since the parser
// does not know which names are variants. A parenthesized single
// pattern is that pattern, as with types.
//
//	_                        WildcardPat
//	'0'..='9' | '.'          OrPat{RangePat, LitPat}
//	Bin(Plus, l, r)          VariantPat{Path: Bin, Args: [IdentPat x3]}
//	(Connecting(n, _), Timeout)   TuplePat{VariantPat, IdentPat}
func (p *Parser) parsePattern() ast.Pattern {
	pat := p.parseSinglePattern()
	if p.tok != token.OR {
		return pat
	}

	or := &ast.OrPat{Alts: []ast.Pattern{pat}}
	for p.tok == token.OR {
		p.next()
		or.Alts = append(or.Alts, p.parseSinglePattern())
	}

	return or
}

func (p *Parser) parseSinglePattern() ast.Pattern {
	switch p.tok {
	case token.IDENT:
		return p.parsePathPattern()
	case token.INT, token.FLOAT, token.CHAR, token.STRING, token.SUB:
		lit := p.parseLitPattern()
		if p.tok != token.RANGEINC {
			return &ast.LitPat{Lit: lit}
		}

		p.next()
		if !p.tok.IsLiteral() && p.tok != token.SUB {
			p.errorExpected(p.pos, "literal after '..='")
			return &ast.RangePat{Lo: lit, Hi: lit}
		}

		return &ast.RangePat{Lo: lit, Hi: p.parseLitPattern()}
	case token.LPAREN:
		pos := p.pos
		elts := p.parsePatternList()
		if len(elts) == 1 {
			return elts[0]
		}

		return &ast.TuplePat{Lparen: pos, Elts: elts}
	}

	p.errorExpected(p.pos, "pattern")
	return &ast.WildcardPat{Underscore: p.pos}
}

// parseLitPattern parses a literal, with an optional minus sign in
// front of a number: -1 matches minus one. The sign becomes part of the
// literal's text, so the checker sees one negative literal.
func (p *Parser) parseLitPattern() *ast.BasicLit {
	lit := &ast.BasicLit{ValuePos: p.pos, Kind: p.tok, Value: p.lit}
	if p.tok != token.SUB {
		p.next()
		return lit
	}

	p.next()
	lit.Kind, lit.Value = p.tok, "-"+p.lit
	if p.tok != token.INT && p.tok != token.FLOAT {
		p.errorExpected(p.pos, "number after '-'")
		lit.Kind = token.INT
		return lit
	}

	p.next()
	return lit
}

// parsePathPattern parses a name, a wildcard, or a variant with its
// sub-patterns: x, _, None, Some(n), pkg.Variant(a, b).
func (p *Parser) parsePathPattern() ast.Pattern {
	name := p.parseIdent()
	if name.Name == "_" {
		return &ast.WildcardPat{Underscore: name.NamePos}
	}

	var path ast.Expr = name
	for p.tok == token.PERIOD {
		p.next()
		path = &ast.SelectorExpr{X: path, Sel: p.parseIdent()}
	}

	if p.tok == token.LPAREN {
		return &ast.VariantPat{Path: path, Args: p.parsePatternList()}
	}

	if _, bare := path.(*ast.Ident); bare {
		return &ast.IdentPat{Name: name}
	}

	return &ast.VariantPat{Path: path} // a qualified constant variant
}

// parsePatternList parses "(" [Pattern {"," Pattern}] ")". A missing
// ")" is reported without consuming the token found instead, so an arm
// such as Some(n => v reports the parenthesis and still finds its "=>".
func (p *Parser) parsePatternList() []ast.Pattern {
	p.expect(token.LPAREN)

	var list []ast.Pattern
	for p.tok != token.RPAREN && p.tok != token.EOF {
		list = append(list, p.parsePattern())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	if p.tok != token.RPAREN {
		p.errorExpected(p.pos, "')' to close the pattern")
		return list
	}

	p.next()
	return list
}

// patternFromExpr converts the left side of an if binding, which was
// read as an expression because the parser could not yet know it was a
// pattern, into that pattern. The forms a binding uses are a name, a
// wildcard, a tuple of them, and a variant with sub-patterns. Anything
// else is reported and becomes a wildcard so the tree stays whole. The
// full pattern grammar is for match arms.
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
