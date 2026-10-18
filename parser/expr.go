// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// exprEnd is the set of tokens that can follow a complete expression
// without being part of one; a return inside ?? has no results when
// one of them comes next.
var exprEnd = newKindSet(token.SEMICOLON, token.COMMA, token.RPAREN, token.RBRACK, token.RBRACE)

// typeArgToken is the set of tokens that may appear inside a type
// argument list. The speculative scan in parseTypeArgsIfAny gives up at
// the first token outside it, which for an ordinary comparison such as
// i < n; is the token right after n.
var typeArgToken = newKindSet(
	token.IDENT, token.SELF, token.PERIOD, token.COMMA, token.LSS, token.GTR, token.SHR,
	token.MUL, token.REF, token.MUT, token.QUESTION, token.LBRACK, token.RBRACK, token.INT,
	token.MAP, token.CHAN, token.ARROW, token.FUNC, token.LPAREN, token.RPAREN,
)

// parseExpr parses an expression. The grammar, from loosest to tightest
// binding, with the precedence of each binary level in token.Precedence:
//
//	Expr    = Unary {binop Unary} | Unary "??" Else
//	Else    = Expr | "return" [ExprList] | "break" | "continue" | Block
//	Unary   = ("-" | "!" | "^" | "<-" | "&" | "*" | "ref" | "mut") Unary | Primary
//	Primary = Operand {"." ident | "[" Index "]" | "(" Args ")" | "<" Types ">" "(" ...
//	        | "{" Elements "}" | "?"}
//	Operand = ident | literal | "(" ExprList ")" | Type | FuncLit | IfExpr | MatchExpr
//
// "??" sits below every other operator and groups to the right, so
// a ?? b == c is a ?? (b == c). The postfix "?" is part of Primary, so
// -x? is -(x?).
func (p *Parser) parseExpr() ast.Expr {
	return p.parseBinaryExpr(token.LowestPrec + 1)
}

// parseExprList parses Expr {"," Expr} up to a closing token: the
// arguments of a call, the elements of a tuple, or the results of a
// return. An empty list is allowed, and a trailing comma before close.
func (p *Parser) parseExprList(close token.Kind) []ast.Expr {
	var list []ast.Expr
	for p.tok != close && p.tok != token.EOF {
		list = append(list, p.parseExpr())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	return list
}

// parseBinaryExpr is precedence climbing: it parses operands joined by
// operators of precedence prec1 or higher, recursing one level tighter
// for each right operand so that a + b * c groups as a + (b * c).
//
//	a + b * c      BinaryExpr{a, +, BinaryExpr{b, *, c}}
//	a ?? b ?? c    CoalesceExpr{a, CoalesceExpr{b, c}}
func (p *Parser) parseBinaryExpr(prec1 int) ast.Expr {
	x := p.parseUnaryExpr()
	for {
		prec := p.tok.Precedence()
		if prec < prec1 {
			return x
		}

		pos, op := p.pos, p.tok
		p.next()
		if op == token.COALESCE {
			x = &ast.CoalesceExpr{X: x, OpPos: pos, Else: p.parseCoalesceElse()}
			continue
		}

		x = &ast.BinaryExpr{X: x, OpPos: pos, Op: op, Y: p.parseBinaryExpr(prec + 1)}
	}
}

// parseCoalesceElse parses what follows "??": an expression, or a
// diverging return, break, continue or block, which is how the examples
// unwrap or bail out in one line.
//
//	s.head ?? return None     ReturnStmt{Results: [None]}
//	d ?? break                BranchStmt{break}
//	cur ?? { *cur = ...; return }   BlockStmt
func (p *Parser) parseCoalesceElse() ast.Node {
	switch p.tok {
	case token.RETURN:
		ret := &ast.ReturnStmt{Return: p.pos}
		p.next()
		if !exprEnd[p.tok] {
			ret.Results = p.parseExprList(token.SEMICOLON)
		}

		return ret
	case token.BREAK, token.CONTINUE:
		pos, tok := p.pos, p.tok
		p.next()
		return &ast.BranchStmt{TokPos: pos, Tok: tok}
	case token.LBRACE:
		return p.skipBody()
	}

	return p.parseBinaryExpr(token.COALESCE.Precedence())
}

// parseUnaryExpr parses prefix operators, which nest: &*p, -(-x). The
// operand of a unary operator is another unary expression, so ref x?
// is ref (x?): the postfix ? binds tighter than any prefix.
//
//	-x        UnaryExpr{-, x}
//	mut s.head    UnaryExpr{mut, SelectorExpr}
//	&Node{..}     UnaryExpr{&, CompositeLit}
//	<-ch      UnaryExpr{<-, ch}
func (p *Parser) parseUnaryExpr() ast.Expr {
	switch p.tok {
	case token.SUB, token.NOT, token.XOR, token.ARROW, token.AND, token.MUL, token.REF, token.MUT:
		pos, op := p.pos, p.tok
		p.next()
		return &ast.UnaryExpr{OpPos: pos, Op: op, X: p.parseUnaryExpr()}
	}

	return p.parsePrimaryExpr()
}

// parsePrimaryExpr parses an operand followed by any number of postfix
// forms: selector, index or slice, call, type arguments, composite
// literal, and the ? that propagates None or Err.
//
//	t.nodes.Get(id)?      TryExpr{CallExpr{SelectorExpr}}
//	list[:n]              SliceExpr
//	arena.New<Node>()     CallExpr{TypeArgs: [Node]}
//	Count{word: k}        CompositeLit{NamedType Count}
func (p *Parser) parsePrimaryExpr() ast.Expr {
	x := p.parseOperand()
	for {
		switch p.tok {
		case token.PERIOD:
			p.next()
			x = &ast.SelectorExpr{X: x, Sel: p.parseIdent()}
		case token.LBRACK:
			x = p.parseIndexOrSlice(x)
		case token.LPAREN:
			x = p.parseCall(x, nil)
		case token.LSS:
			args := p.parseTypeArgsIfAny(x)
			if args == nil {
				return x // an ordinary less-than; the binary loop takes it
			}

			if p.tok == token.LPAREN {
				x = p.parseCall(x, args)
			} else if isLiteralType(x) {
				x = p.parseCompositeLit(namedType(x, args))
			} else {
				p.errorExpected(p.pos, "'('")
			}
		case token.LBRACE:
			// In a control clause a '{' after a name opens the body,
			// but after a type written out, as in range []int{1, 2} {,
			// it can only be a literal. Same rule as go/parser.
			_, written := x.(*ast.TypeExpr)
			if !isLiteralType(x) || p.exprLev < 0 && !written {
				return x
			}

			x = p.parseCompositeLit(literalType(x))
		case token.QUESTION:
			x = &ast.TryExpr{X: x, Question: p.pos}
			p.next()
		default:
			return x
		}
	}
}

// parseTypeArgsIfAny decides whether the "<" after x opens a type
// argument list, and parses it if so. It returns nil, consuming
// nothing, when the "<" is a less-than.
//
// The rule, written in the syntax conventions: it is a type argument
// list only when x is a name, the tokens up to the matching ">" are all
// tokens that can occur in a type, and that ">" is directly followed by
// "(" or "{". The decision is made by a scan over tokens that builds no
// nodes, so the common case, a comparison like i < n, costs a few token
// reads and no allocation. Only when the scan succeeds is the list
// parsed for real.
func (p *Parser) parseTypeArgsIfAny(x ast.Expr) []ast.Type {
	switch x.(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		return nil
	}

	saved := p.save()
	depth := 0
	for typeArgToken[p.tok] {
		switch p.tok {
		case token.LSS:
			depth++
		case token.GTR:
			depth--
		case token.SHR:
			depth -= 2
		}

		p.next()
		if depth <= 0 {
			break
		}
	}

	ok := depth == 0 && (p.tok == token.LPAREN || p.tok == token.LBRACE && p.exprLev >= 0)
	p.restore(saved)
	if !ok {
		return nil
	}

	return p.parseTypeArgs()
}

// parseOperand parses the start of a primary expression.
//
//	x, 42, "s", 'c'     Ident, BasicLit
//	(a, b), (x)         TupleExpr, ParenExpr
//	[]int, map[K]V      TypeExpr, usually followed by a literal body or a call
//	func(x int) int { }     FuncLit
//	if c { a } else { b }   IfExpr
//	match e { ... }         MatchExpr
func (p *Parser) parseOperand() ast.Expr {
	switch p.tok {
	case token.IDENT:
		return p.parseIdent()
	case token.SUPER:
		x := &ast.Ident{NamePos: p.pos, Name: p.lit}
		p.next()
		return x
	case token.INT, token.FLOAT, token.CHAR, token.STRING:
		x := &ast.BasicLit{ValuePos: p.pos, Kind: p.tok, Value: p.lit}
		p.next()
		return x
	case token.LPAREN:
		return p.parseParenOrTuple()
	case token.LBRACK, token.MAP, token.CHAN, token.STRUCT:
		return &ast.TypeExpr{Type: p.parseType()}
	case token.FUNC:
		typ := p.parseFuncType()
		if p.tok != token.LBRACE {
			return &ast.TypeExpr{Type: typ}
		}

		return &ast.FuncLit{Type: typ, Body: p.skipBody()}
	case token.IF:
		return p.parseIfExpr()
	case token.MATCH:
		return p.parseMatchExpr()
	}

	p.errorExpected(p.pos, "expression")
	return &ast.BadExpr{From: p.pos, To: p.pos}
}

// parseParenOrTuple parses "(" [ExprList] ")": a parenthesized
// expression when there is exactly one, a tuple otherwise, including
// the empty tuple () that is the unit value in Ok(()).
func (p *Parser) parseParenOrTuple() ast.Expr {
	pos, list := p.parseParenExprs()
	if len(list) == 1 {
		return &ast.ParenExpr{Lparen: pos, X: list[0]}
	}

	return &ast.TupleExpr{Lparen: pos, Elts: list}
}

// parseIndexOrSlice parses "[" Expr "]" or "[" [Expr] ":" [Expr] "]".
func (p *Parser) parseIndexOrSlice(x ast.Expr) ast.Expr {
	pos := p.expect(token.LBRACK)
	p.exprLev++

	var low, high ast.Expr
	if p.tok != token.COLON {
		low = p.parseExpr()
	}

	isSlice := p.tok == token.COLON
	if isSlice {
		p.next()
		if p.tok != token.RBRACK {
			high = p.parseExpr()
		}
	}

	p.exprLev--
	p.expect(token.RBRACK)
	if isSlice {
		return &ast.SliceExpr{X: x, Lbrack: pos, Low: low, High: high}
	}

	return &ast.IndexExpr{X: x, Lbrack: pos, Index: low}
}

// parseCall parses "(" [ExprList] ")" after the function and any type
// arguments already read.
func (p *Parser) parseCall(fun ast.Expr, typeArgs []ast.Type) *ast.CallExpr {
	pos, args := p.parseParenExprs()
	return &ast.CallExpr{Fun: fun, TypeArgs: typeArgs, Lparen: pos, Args: args}
}

// parseParenExprs parses "(" [ExprList] ")" and returns the position
// of the opening parenthesis with the list. Inside the parentheses the
// expression level is raised, so a composite literal is allowed again
// even within a control clause: if f(T{}) {.
func (p *Parser) parseParenExprs() (token.Pos, []ast.Expr) {
	pos := p.expect(token.LPAREN)
	p.exprLev++
	list := p.parseExprList(token.RPAREN)
	p.exprLev--
	p.expect(token.RPAREN)
	return pos, list
}

// parseCompositeLit parses "{" [Element {"," Element}] "}" for a type
// already read, or for no type at all when the literal is an element of
// another literal: []Point{{1, 2}, {3, 4}}.
func (p *Parser) parseCompositeLit(typ ast.Type) *ast.CompositeLit {
	lit := &ast.CompositeLit{Type: typ, Lbrace: p.expect(token.LBRACE)}
	p.exprLev++
	for p.tok != token.RBRACE && p.tok != token.EOF {
		lit.Elts = append(lit.Elts, p.parseElement())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	p.exprLev--
	p.expect(token.RBRACE)
	return lit
}

// parseElement parses one element of a composite literal: a value, or
// key ":" value, where either side may be a literal with its type
// elided.
func (p *Parser) parseElement() ast.Expr {
	x := p.parseElementValue()
	if p.tok != token.COLON {
		return x
	}

	p.next()
	return &ast.KeyValueExpr{Key: x, Value: p.parseElementValue()}
}

func (p *Parser) parseElementValue() ast.Expr {
	if p.tok == token.LBRACE {
		return p.parseCompositeLit(nil)
	}

	return p.parseExpr()
}

// parseIfExpr parses "if" Expr Block "else" (Block | IfExpr) in
// expression position. The condition is a control clause, so a "{" in
// it opens the body. The blocks are skipped until task 0.8.
//
//	if ci { "(?i)" + p } else { p.Clone() }
func (p *Parser) parseIfExpr() *ast.IfExpr {
	x := &ast.IfExpr{If: p.expect(token.IF)}
	x.Cond = p.parseControlExpr()
	x.Then = p.skipBody()
	if p.tok != token.ELSE {
		p.errorExpected(p.pos, "'else'") // an if expression has a value on every path
		return x
	}

	p.next()
	if p.tok == token.IF {
		x.Else = p.parseIfExpr()
	} else {
		x.Else = p.skipBody()
	}

	return x
}

// parseMatchExpr parses "match" Expr "{" arms "}". The arms arrive with
// task 0.10; until then the body is skipped and Arms is nil.
func (p *Parser) parseMatchExpr() *ast.MatchExpr {
	x := &ast.MatchExpr{Match: p.expect(token.MATCH)}
	x.X = p.parseControlExpr()
	p.skipBody()
	return x
}

// parseControlExpr parses the expression of a control clause, where a
// "{" ends the expression instead of opening a composite literal.
func (p *Parser) parseControlExpr() ast.Expr {
	prev := p.exprLev
	p.exprLev = -1
	x := p.parseExpr()
	p.exprLev = prev
	return x
}

// isLiteralType reports whether x can be the type of a composite
// literal: a name, possibly qualified, or a slice, array, map or struct
// type written out.
func isLiteralType(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		_, ok := x.X.(*ast.Ident)
		return ok
	case *ast.TypeExpr:
		switch x.Type.(type) {
		case *ast.SliceType, *ast.ArrayType, *ast.MapType, *ast.StructType:
			return true
		}
	}

	return false
}

// literalType converts an operand that isLiteralType accepted into the
// type node a CompositeLit carries.
func literalType(x ast.Expr) ast.Type {
	if t, ok := x.(*ast.TypeExpr); ok {
		return t.Type
	}

	return namedType(x, nil)
}

// namedType converts a name read as an expression, x or pkg.x, into
// the NamedType it denotes, with the given type arguments.
func namedType(x ast.Expr, args []ast.Type) *ast.NamedType {
	switch x := x.(type) {
	case *ast.Ident:
		return &ast.NamedType{Name: x, Args: args}
	case *ast.SelectorExpr:
		return &ast.NamedType{Pkg: x.X.(*ast.Ident), Name: x.Sel, Args: args}
	}

	panic("namedType: not a name")
}
