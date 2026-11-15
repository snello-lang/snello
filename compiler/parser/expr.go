// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"slices"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
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
	token.TYPE, // the type of a field a loop over a type's fields gives, f.type
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

// parseExprList parses Expr {"," Expr}, at least one expression. A
// trailing comma is allowed before close, as in a call whose arguments
// end on their own lines; close is ILLEGAL, which no token is, where a
// trailing comma is not allowed. A caller that accepts an empty list
// checks for its closing token before calling.
func (p *Parser) parseExprList(close token.Kind) []ast.Expr {
	list := []ast.Expr{p.parseExpr()}
	for p.tok == token.COMMA {
		p.next()
		if p.tok == close {
			break
		}

		list = append(list, p.parseExpr())
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
	case token.RETURN: // one result, as a return in an arm: a comma after it ends the arm or the element it stands in; several are a tuple
		ret := &ast.ReturnStmt{Return: p.pos}
		p.next()
		if !exprEnd[p.tok] {
			ret.Results = []ast.Expr{p.parseExpr()}
		}

		return ret
	case token.BREAK, token.CONTINUE, token.LBRACE:
		return p.parseStmt()
	}

	return p.parseBinaryExpr(token.COALESCE.Precedence())
}

// parseUnaryExpr parses prefix operators, which nest: &*p, -(-x). Its
// operator set is mirrored in exprStart. The
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
			if p.tok == token.LBRACK { // x.[f], a part a loop over a type's parts gives
				part := &ast.PartExpr{X: x, Lbrack: p.pos}
				p.next()
				part.Part = p.parseIdent()
				part.Rbrack = p.expect(token.RBRACK)
				x = part
				continue
			}

			x = &ast.SelectorExpr{X: x, Sel: p.parseNameOrType()}
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
			} else if p.tok == token.PERIOD { // Pair<int, string>.Of(...): an instance, then a function of it
				x = &ast.TypeExpr{Type: namedType(x, args)}
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
// "(", "{" or ".", the last naming a function of the instance,
// Pair<int, string>.Of(...). The decision is made by a scan over tokens that builds no
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

	ok := depth == 0 && (p.tok == token.LPAREN || p.tok == token.LBRACE && p.exprLev >= 0 || p.tok == token.PERIOD)
	p.restore(saved)
	if !ok {
		return nil
	}

	return p.parseTypeArgs()
}

// parseOperand parses the start of a primary expression. Its cases are
// mirrored in exprStart, which decides what may begin a statement.
//
//	x, 42, "s", 'c'     Ident, BasicLit
//	(a, b), (x)         TupleExpr, ParenExpr
//	[]int, map[K]V      TypeExpr, usually followed by a literal body or a call
//	Self                TypeExpr, followed by a function of the type, Self.Count()
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
	case token.LBRACK, token.MAP, token.CHAN, token.STRUCT, token.SELF:
		return &ast.TypeExpr{Type: p.parseType()}
	case token.FUNC:
		typ := p.parseFuncType()
		if p.tok != token.LBRACE {
			return &ast.TypeExpr{Type: typ}
		}

		return &ast.FuncLit{Type: typ, Body: p.parseBlock()}
	case token.IF:
		return p.parseIfExpr()
	case token.MATCH:
		return p.parseMatchExpr()
	case token.SWITCH:
		bad := p.refuseSwitch()
		return &ast.BadExpr{From: bad.From, To: bad.To}
	}

	p.errorExpected(p.pos, "expression")
	return &ast.BadExpr{From: p.pos, To: p.pos}
}

// parseParenOrTuple parses "(" [ExprList] ")": a parenthesized
// expression when there is exactly one, a tuple otherwise, including
// the empty tuple () that is the unit value in Ok(()).
func (p *Parser) parseParenOrTuple() ast.Expr {
	pos, list, _ := p.parseParenExprs()
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

// parseCall parses "(" [ExprList ["..."]] ")" after the function and
// any type arguments already read: the ... after the last argument
// passes a slice as the variadic parameter, f(xs...). A ... after
// another argument is reported, and the arguments after it are still
// parsed. Inside the parentheses the expression level is raised, as in
// parseParenExprs.
func (p *Parser) parseCall(fun ast.Expr, typeArgs []ast.Type) *ast.CallExpr {
	call := &ast.CallExpr{Fun: fun, TypeArgs: typeArgs, Lparen: p.expect(token.LPAREN)}
	p.exprLev++
	if p.tok == token.FOR { // c(for f := range fields(c) { value }), a variant built from its fields
		call.Args = []ast.Expr{p.parsePartsElt()}
	} else if p.tok != token.RPAREN {
		call.Args = p.parseExprList(token.RPAREN)
	}

	if p.tok == token.ELLIPSIS {
		call.Ellipsis = p.pos
		p.next()
		if p.tok == token.COMMA {
			p.next()
		}

		if p.tok != token.RPAREN && p.tok != token.EOF {
			p.error(call.Ellipsis, "can only use ... with the final argument")
			call.Ellipsis = token.NoPos
			call.Args = append(call.Args, p.parseExprList(token.RPAREN)...)
		}
	}

	p.exprLev--
	call.Rparen = p.expect(token.RPAREN)
	return call
}

// parseParenExprs parses "(" [ExprList] ")" and returns the positions
// of the parentheses with the list. Inside the parentheses the
// expression level is raised, so a composite literal is allowed again
// even within a control clause: if f(T{}) {.
func (p *Parser) parseParenExprs() (token.Pos, []ast.Expr, token.Pos) {
	pos := p.expect(token.LPAREN)
	p.exprLev++

	var list []ast.Expr
	if p.tok != token.RPAREN {
		list = p.parseExprList(token.RPAREN)
	}

	p.exprLev--
	return pos, list, p.expect(token.RPAREN)
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
// elided, or an element written once for each part of a type.
func (p *Parser) parseElement() ast.Expr {
	if p.tok == token.FOR {
		return p.parsePartsElt()
	}

	x := p.parseElementValue()
	if p.tok != token.COLON {
		return x
	}

	p.next()
	return &ast.KeyValueExpr{Key: x, Value: p.parseElementValue()}
}

// parsePartsElt parses "for" ident ":=" "range" Expr "{" ["[" ident
// "]" ":"] Value "}", an element of a literal written once for each
// part the range gives: a field and its value, or an array's element.
//
//	for f := range fields(T) { [f]: parse(mut r)? }
func (p *Parser) parsePartsElt() *ast.PartsElt {
	elt := &ast.PartsElt{For: p.expect(token.FOR)}
	elt.Var = p.parseIdent()
	p.expect(token.DEFINE)
	p.expect(token.RANGE)
	elt.X = p.parseControlExpr()
	p.expect(token.LBRACE)
	if p.tok == token.LBRACK {
		p.next()
		elt.Key = p.parseIdent()
		p.expect(token.RBRACK)
		p.expect(token.COLON)
	}

	elt.Value = p.parseElementValue()
	if p.tok == token.SEMICOLON { // a newline before the }
		p.next()
	}

	p.expect(token.RBRACE)
	return elt
}

func (p *Parser) parseElementValue() ast.Expr {
	if p.tok == token.LBRACE {
		return p.parseCompositeLit(nil)
	}

	return p.parseExpr()
}

// parseIfExpr parses "if" Expr Block "else" (Block | IfExpr) in
// expression position. The condition is a control clause, so a "{" in
// it opens the body.
//
//	if ci { "(?i)" + p } else { p.Clone() }
func (p *Parser) parseIfExpr() *ast.IfExpr {
	x := &ast.IfExpr{If: p.expect(token.IF)}
	x.Cond = p.parseControlExpr()
	x.Then = p.parseBlock()
	if p.tok != token.ELSE {
		p.errorExpected(p.pos, "'else'") // an if expression has a value on every path
		return x
	}

	p.next()
	if p.tok == token.IF {
		x.Else = p.parseIfExpr()
	} else {
		x.Else = p.parseBlock()
	}

	return x
}

// parseMatchExpr parses "match" Expr "{" {Arm} "}". The value is a
// control clause, so a "{" in it opens the arms. Arms end with a comma
// or a newline, and the last may have neither.
//
//	match (s, ev) {
//	    (Closed, Dial)                       => Connecting(1, now),
//	    (Connecting(n, _), Timeout) if n < 3 => Connecting(n + 1, now),
//	    _                                    => s,
//	}
//
// A match with no value, "match" "{" {Cond "=>" Body} "}", takes each
// arm's condition in turn and runs the first that holds, ending with
// the arm _ for when none does:
//
//	match {
//	    score >= 90 => "A",
//	    score >= 80 => "B",
//	    _           => "F",
//	}
//
// It is parsed as a match on the unit, each condition the guard of a
// wildcard, so everything after the parser sees one shape.
func (p *Parser) parseMatchExpr() *ast.MatchExpr {
	x := &ast.MatchExpr{Match: p.expect(token.MATCH)}
	if p.tok == token.LBRACE {
		x.Conds = true
		x.X = &ast.TupleExpr{Lparen: x.Match}
	} else {
		x.X = p.parseControlExpr()
		x.OnType = p.namesType(x.X)
		if t, ok := x.X.(*ast.TypeExpr); ok {
			if _, self := t.Type.(*ast.SelfType); self { // its arms are read as a type's, so that this is the one error
				p.error(t.Pos(), "a match on a type is on a type parameter by its name, and Self is none: pass it to a generic function, describe<Self>(s), and match there")
			}
		}
	}

	if !p.expectLbrace() {
		return x
	}

	prev := p.exprLev // the arms are a body of their own: a '{' in them opens a literal, wherever the match stands
	p.exprLev = 0
	defer func() { p.exprLev = prev }()
	for p.tok != token.RBRACE && p.tok != token.EOF {
		if x.Conds {
			x.Arms = append(x.Arms, p.parseCondArm())
		} else if x.OnType {
			x.Arms = append(x.Arms, p.parseTypeArm())
		} else {
			x.Arms = append(x.Arms, p.parseMatchArm())
		}

		switch p.tok {
		case token.COMMA, token.SEMICOLON:
			p.next()
		case token.RBRACE:
		default:
			p.errorExpected(p.pos, "',' or newline after the arm")
			p.skipTo(armEnd)
			if p.tok == token.COMMA || p.tok == token.SEMICOLON {
				p.next()
			}
		}
	}

	p.expect(token.RBRACE)
	return x
}

// parseMatchArm parses Pattern ["if" Expr] "=>" Body. An arm without
// its "=>" is reported once and skipped to the end of the arm, with
// Body left nil.
func (p *Parser) parseMatchArm() *ast.MatchArm {
	if p.tok == token.FOR {
		return p.parseArmRest(&ast.MatchArm{Pat: p.parsePartsPat()})
	}

	arm := &ast.MatchArm{Pat: p.parsePattern()}
	if p.tok == token.IF {
		p.next()
		arm.Guard = p.parseExpr()
	}

	return p.parseArmRest(arm)
}

// namesType reports whether x, the value of a match, names a type the
// match is on: a type parameter in scope, the function's, its type's,
// its trait's or a shape's, or a field's type, f.type; or Self, which
// parseMatchExpr refuses.
func (p *Parser) namesType(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return isTypeParam(p.fn, x.Name) || p.traitParam(x.Name) || slices.Contains(p.shapes, x.Name)
	case *ast.TypeExpr:
		_, self := x.Type.(*ast.SelfType)
		return self
	case *ast.SelectorExpr:
		return x.Sel.Name == "type"
	}

	return false
}

// traitParam reports whether name is a type parameter of the trait whose
// default bodies are being parsed, T of trait Kinds<T>.
func (p *Parser) traitParam(name string) bool {
	if p.trait == nil {
		return false
	}

	for _, tp := range p.trait.TypeParams {
		if tp.Name.Name == name {
			return true
		}
	}

	return false
}

// parseTypeArm parses an arm of a match on a type parameter: _, a kind
// of type, struct or enum, or types joined by |, each a type or a shape
// such as []E; then "=>" and the body. A type is matched by
// what it is, so the arm takes no guard.
//
//	i8 | i16 => ...   TypePat{Types: [i8, i16]}
//	struct => ...     TypePat{Kind: "struct"}
func (p *Parser) parseTypeArm() *ast.MatchArm {
	arm := &ast.MatchArm{}
	switch {
	case p.tok == token.IDENT && p.lit == "_":
		arm.Pat = &ast.WildcardPat{Underscore: p.pos}
		p.next()
	case p.tok == token.STRUCT || p.tok == token.ENUM:
		arm.Pat = &ast.TypePat{KindPos: p.pos, Kind: p.tok.String()}
		p.next()
	default:
		pat := &ast.TypePat{Types: []ast.Type{p.parseType()}}
		for p.tok == token.OR {
			p.next()
			pat.Types = append(pat.Types, p.parseType())
		}

		arm.Pat = pat
	}

	if p.tok == token.IF {
		p.error(p.pos, "an arm of a match on a type takes no guard: a type is matched by what it is")
		p.next()
		p.parseExpr()
	}

	outer := p.shapes
	if pat, ok := arm.Pat.(*ast.TypePat); ok { // a shape's names are type parameters in its body, E in []E
		p.shapes = shapeNames(pat.Types, outer)
	}

	arm = p.parseArmRest(arm)
	p.shapes = outer
	return arm
}

// shapeNames returns outer and every plain name in ts, the names a
// shape may bind; one that is a type of its own is not matched on, and
// the checker says so.
func shapeNames(ts []ast.Type, outer []string) []string {
	names := outer
	var walk func(t ast.Type)
	walk = func(t ast.Type) {
		switch t := t.(type) {
		case *ast.NamedType:
			if t.Pkg == nil && t.Args == nil {
				if len(names) == len(outer) {
					names = append([]string(nil), outer...)
				}

				names = append(names, t.Name.Name)
			}

			for _, a := range t.Args {
				walk(a)
			}
		case *ast.OptionType:
			walk(t.Elem)
		case *ast.SliceType:
			walk(t.Elem)
		case *ast.PointerType:
			walk(t.Elem)
		case *ast.MapType:
			walk(t.Key)
			walk(t.Value)
		}
	}

	for _, t := range ts {
		walk(t)
	}

	return names
}

// parsePartsPat parses "for" ident ":=" "range" Expr, the head of an
// arm written once for each variant of an enum, before its "=>".
//
//	for c := range variants(T) => ...   PartsPat{Var: c, X: variants(T)}
func (p *Parser) parsePartsPat() *ast.PartsPat {
	pat := &ast.PartsPat{For: p.expect(token.FOR)}
	pat.Var = p.parseIdent()
	p.expect(token.DEFINE)
	p.expect(token.RANGE)
	pat.X = p.parseExpr()
	return pat
}

// parseCondArm parses an arm of a match with no value, Cond "=>" Body,
// as the guard of a wildcard, or _ "=>" Body as the wildcard alone. The
// condition is the arm's whole test, so an "if" after it is reported:
// two conditions are joined with &&.
func (p *Parser) parseCondArm() *ast.MatchArm {
	cond := p.parseExpr()
	arm := &ast.MatchArm{Pat: &ast.WildcardPat{Underscore: cond.Pos()}}
	if id, ok := cond.(*ast.Ident); !ok || id.Name != "_" {
		arm.Guard = cond
	}

	if p.tok == token.IF {
		p.error(p.pos, "an arm of a match with no value is its condition alone: join two with &&")
		p.next()
		p.parseExpr()
	}

	return p.parseArmRest(arm)
}

// parseArmRest parses the "=>" and the body of an arm whose head is
// parsed.
func (p *Parser) parseArmRest(arm *ast.MatchArm) *ast.MatchArm {
	if p.tok != token.FATARROW {
		p.errorExpected(p.pos, "'=>'")
		p.skipTo(armEnd)
		return arm
	}

	arm.Arrow = p.pos
	p.next()
	arm.Body = p.parseArmBody()
	return arm
}

// parseArmBody parses what follows "=>": a block, a return, break or
// continue, a send or assignment, or an expression, which the examples
// use freely: `Stop => return,` and `Balance(reply) => reply <- n,`.
// A plain expression is returned as the expression itself.
func (p *Parser) parseArmBody() ast.Node {
	switch p.tok {
	case token.LBRACE, token.BREAK, token.CONTINUE:
		return p.parseStmt()
	case token.RETURN:
		// A comma ends the arm, so a return here carries at most one
		// expression; several results are written as a tuple.
		ret := &ast.ReturnStmt{Return: p.pos}
		p.next()
		if !exprEnd[p.tok] {
			ret.Results = []ast.Expr{p.parseExpr()}
		}

		return ret
	}

	x := p.parseExpr()
	s := p.simpleStmtAfter(x, nil)
	if es, ok := s.(*ast.ExprStmt); ok {
		return es.X
	}

	return s
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
