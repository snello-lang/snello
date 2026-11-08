// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// exprStart is the set of tokens that can begin an expression, and so
// a simple statement: the cases of parseOperand and parseUnaryExpr,
// which must be kept in step with it. A statement beginning with any
// other token is reported and skipped; without this check a stray ')'
// would yield a placeholder expression that consumes nothing, and the
// block loop would never advance.
var exprStart = newKindSet(
	token.IDENT, token.SUPER, token.INT, token.FLOAT, token.CHAR, token.STRING,
	token.LPAREN, token.LBRACK, token.MAP, token.CHAN, token.STRUCT, token.FUNC, token.IF, token.MATCH,
	token.SUB, token.NOT, token.XOR, token.ARROW, token.AND, token.MUL, token.REF, token.MUT,
)

// parseBlock parses "{" StmtList "}".
func (p *Parser) parseBlock() *ast.BlockStmt {
	block := &ast.BlockStmt{Lbrace: p.pos}
	if !p.expectLbrace() {
		return block
	}

	block.List = p.parseStmtList()
	block.Rbrace = p.expect(token.RBRACE)
	return block
}

// parseStmtList parses statements up to a closing brace or the next
// case of a select. Each statement ends with a semicolon,
// usually the one the scanner inserts at the end of its line. After a
// statement that fails, the rest of its line is skipped, and parsing
// resumes at the next line or at a keyword that can only begin a
// statement.
func (p *Parser) parseStmtList() []ast.Stmt {
	var list []ast.Stmt
	for p.tok != token.RBRACE && p.tok != token.EOF && p.tok != token.CASE && p.tok != token.DEFAULT {
		list = append(list, p.parseStmt())
		if p.expectSemi() {
			continue
		}

		p.skipTo(stmtEnd)
		if p.tok == token.SEMICOLON {
			p.next()
		}
	}

	return list
}

// parseStmt parses one statement, dispatching on its first token. A
// statement that begins with no keyword is a simple statement: an
// expression, an assignment, a send, or x++.
//
//	var x int           DeclStmt
//	spawn worker(id)    SpawnStmt
//	return a, b         ReturnStmt
//	{ ... }             BlockStmt
//	x := f()            AssignStmt, via parseSimpleStmt
func (p *Parser) parseStmt() ast.Stmt {
	switch p.tok {
	case token.VAR, token.CONST:
		return &ast.DeclStmt{Decl: p.parseVarDecl()}
	case token.SPAWN:
		pos := p.pos
		p.next()
		return &ast.SpawnStmt{Spawn: pos, Call: p.parseCallExpr("spawn")}
	case token.DEFER:
		pos := p.pos
		p.next()
		return &ast.DeferStmt{Defer: pos, Call: p.parseCallExpr("defer")}
	case token.RETURN:
		ret := &ast.ReturnStmt{Return: p.pos}
		p.next()
		if !exprEnd[p.tok] {
			ret.Results = p.parseExprList(token.SEMICOLON)
		}

		return ret
	case token.BREAK, token.CONTINUE:
		s := &ast.BranchStmt{TokPos: p.pos, Tok: p.tok}
		p.next()
		return s
	case token.IF:
		return p.parseIfStmt()
	case token.FOR:
		return p.parseForStmt()
	case token.SWITCH:
		return p.refuseSwitch()
	case token.SELECT:
		return p.parseSelectStmt()
	case token.LBRACE:
		return p.parseBlock()
	case token.SEMICOLON:
		s := &ast.EmptyStmt{Semicolon: p.pos}
		p.next()
		return s
	}

	if exprStart[p.tok] {
		return p.parseSimpleStmt()
	}

	bad := &ast.BadStmt{From: p.pos}
	p.errorExpected(p.pos, "statement")
	p.skipTo(stmtEnd)
	bad.To = p.pos
	return bad
}

// parseCallExpr parses the expression after spawn or defer and checks
// that it is a call, as go/parser does for go and defer.
func (p *Parser) parseCallExpr(keyword string) ast.Expr {
	x := p.parseExpr()
	if _, ok := x.(*ast.CallExpr); !ok {
		p.error(x.Pos(), "expression in "+keyword+" must be a function call")
	}

	return x
}

// parseSimpleStmt parses a statement that begins with an expression.
// The expression list on the left decides nothing by itself; the token
// after it does.
//
//	f(x)                ExprStmt
//	a, b := x, y        AssignStmt{Tok: DEFINE}
//	n += 1              AssignStmt{Tok: ADD_ASSIGN}
//	i++                 IncDecStmt
//	out <- r            SendStmt
//	k, v := range m     RangeStmt, only inside a for header
func (p *Parser) parseSimpleStmt() ast.Stmt {
	// Most simple statements have one expression on the left and keep
	// it directly, so the list is built only when a comma or an
	// assignment calls for one.
	x := p.parseExpr()
	var lhs []ast.Expr
	if p.tok == token.COMMA {
		lhs = []ast.Expr{x}
		for p.tok == token.COMMA {
			p.next()
			lhs = append(lhs, p.parseExpr())
		}
	}

	return p.simpleStmtAfter(x, lhs)
}

// simpleStmtAfter builds the simple statement that the token after an
// expression list calls for. x is the first expression; lhs is the
// whole list when there was a comma, nil otherwise. A match arm body
// uses it too, with lhs nil, since a comma there ends the arm.
func (p *Parser) simpleStmtAfter(x ast.Expr, lhs []ast.Expr) ast.Stmt {
	switch p.tok {
	case token.DEFINE, token.ASSIGN,
		token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN, token.REM_ASSIGN,
		token.AND_ASSIGN, token.OR_ASSIGN, token.XOR_ASSIGN, token.SHL_ASSIGN, token.SHR_ASSIGN, token.AND_NOT_ASSIGN:
		pos, tok := p.pos, p.tok
		p.next()
		if lhs == nil {
			lhs = []ast.Expr{x}
		}

		if p.tok == token.RANGE {
			if tok != token.DEFINE && tok != token.ASSIGN {
				p.error(pos, "'range' needs ':=' or '='")
			}

			return p.parseRangeClause(lhs, tok)
		}

		// One name on the left takes one expression on the right, as
		// in Go; only a list takes a list. This is also what lets a
		// comma end a match arm whose body is an assignment.
		var rhs []ast.Expr
		if len(lhs) == 1 {
			rhs = []ast.Expr{p.parseExpr()}
		} else {
			rhs = p.parseExprList(token.ILLEGAL)
		}

		return &ast.AssignStmt{Lhs: lhs, TokPos: pos, Tok: tok, Rhs: rhs}
	case token.INC, token.DEC:
		s := &ast.IncDecStmt{X: x, TokPos: p.pos, Tok: p.tok}
		p.next()
		return s
	case token.ARROW:
		pos := p.pos
		p.next()
		return &ast.SendStmt{Chan: x, Arrow: pos, Value: p.parseExpr()}
	}

	if lhs != nil {
		p.errorExpected(p.pos, "':=' or '='")
	}

	return &ast.ExprStmt{X: x}
}

// parseRangeClause parses "range" Expr after the variables and the
// := or = of a for header. With one variable it goes in Key, whatever
// it means for the ranged value; the checker decides.
func (p *Parser) parseRangeClause(vars []ast.Expr, tok token.Kind) *ast.RangeStmt {
	p.expect(token.RANGE)
	s := &ast.RangeStmt{Tok: tok, X: p.parseExpr()}
	if len(vars) > 2 {
		p.error(vars[2].Pos(), "range allows at most two variables")
		vars = vars[:2]
	}

	if len(vars) > 0 {
		s.Key = vars[0]
	}

	if len(vars) > 1 {
		s.Value = vars[1]
	}

	return s
}

// parseIfStmt parses Go's if, with an optional init statement, and
// Snello's option binding. Both begin with a simple statement; what
// follows it tells them apart:
//
//	if x > 0 {                  IfStmt{Cond}
//	if v := f(); v > 0 {        IfStmt{Init, Cond}: a ';' after the statement
//	if x := opt {               IfBindStmt{Pat x, Value opt}: a '{' right after :=
//	if (k, v) := pair {         IfBindStmt with a tuple pattern
//
// The else branch is another if, a binding if, or a block.
func (p *Parser) parseIfStmt() ast.Stmt {
	pos := p.expect(token.IF)
	prev := p.exprLev
	p.exprLev = -1

	var init ast.Stmt
	var cond ast.Expr
	if p.tok == token.LBRACE {
		p.errorExpected(p.pos, "condition")
	} else {
		init = p.parseSimpleStmt()
	}

	if bind, ok := init.(*ast.AssignStmt); ok && bind.Tok == token.DEFINE && p.tok == token.LBRACE {
		p.exprLev = prev
		return p.parseIfBindRest(pos, bind)
	}

	if p.tok == token.SEMICOLON {
		p.next()
		cond = p.parseExpr()
	} else if init != nil {
		cond = p.condExpr(init)
		init = nil
	}

	p.exprLev = prev
	s := &ast.IfStmt{If: pos, Init: init, Cond: cond, Body: p.parseBlock()}
	s.Else = p.parseElse()
	return s
}

// parseIfBindRest finishes an if binding from its parsed header.
func (p *Parser) parseIfBindRest(pos token.Pos, bind *ast.AssignStmt) *ast.IfBindStmt {
	s := &ast.IfBindStmt{If: pos}
	if len(bind.Lhs) != 1 || len(bind.Rhs) != 1 {
		p.error(bind.TokPos, "an if binding has one pattern and one value")
	}

	s.Pat = p.patternFromExpr(bind.Lhs[0])
	s.Value = bind.Rhs[0]
	s.Body = p.parseBlock()
	s.Else = p.parseElse()
	return s
}

// parseElse parses an optional else branch.
func (p *Parser) parseElse() ast.Stmt {
	if p.tok != token.ELSE {
		return nil
	}

	p.next()
	switch p.tok {
	case token.IF:
		return p.parseIfStmt()
	case token.LBRACE:
		return p.parseBlock()
	}

	p.errorExpected(p.pos, "'if' or '{'")
	return nil
}

// condExpr returns the expression of a simple statement used where a
// condition is expected, reporting any other kind of statement.
func (p *Parser) condExpr(s ast.Stmt) ast.Expr {
	if es, ok := s.(*ast.ExprStmt); ok {
		return es.X
	}

	p.error(s.Pos(), "expected a condition, found a statement")
	return &ast.BadExpr{From: s.Pos(), To: s.Pos()}
}

// parseForStmt parses every for form:
//
//	for {                        ForStmt, no clauses
//	for cond {                   ForStmt{Cond}
//	for i := 0; i < n; i++ {     ForStmt{Init, Cond, Post}
//	for k, v := range m {        RangeStmt
//	for range ch {               RangeStmt, no variables
func (p *Parser) parseForStmt() ast.Stmt {
	pos := p.expect(token.FOR)
	prev := p.exprLev
	p.exprLev = -1

	// The header is one of: nothing, a range clause, a condition, or
	// init; cond; post. A range clause may also arrive as the result
	// of parsing the first simple statement.
	var init, post ast.Stmt
	var cond ast.Expr
	var rng *ast.RangeStmt
	switch {
	case p.tok == token.RANGE:
		rng = p.parseRangeClause(nil, token.ILLEGAL)
	case p.tok != token.LBRACE && p.tok != token.SEMICOLON:
		init = p.parseSimpleStmt()
		rng, _ = init.(*ast.RangeStmt)
	}

	switch {
	case rng != nil:
	case p.tok == token.SEMICOLON:
		p.next()
		if p.tok != token.SEMICOLON {
			cond = p.parseExpr()
		}

		if p.tok != token.SEMICOLON {
			p.errorExpected(p.pos, "';'") // leave the '{' for the body
		} else {
			p.next()
			if p.tok != token.LBRACE {
				post = p.parseSimpleStmt()
			}
		}
	case init != nil:
		cond = p.condExpr(init)
		init = nil
	}

	p.exprLev = prev
	if rng != nil {
		rng.For = pos
		rng.Body = p.parseBlock()
		return rng
	}

	return &ast.ForStmt{For: pos, Init: init, Cond: cond, Post: post, Body: p.parseBlock()}
}

// noSwitch is the message for a switch, which Snello does not have:
// match does what a switch does, on a value, and with no value on
// conditions, the first that holds.
const noSwitch = "Snello has no switch: use match, on a value, match x { 1 | 2 => ... }, or on conditions, match { x > 0 => ... }"

// refuseSwitch reports a switch and skips it whole, wherever it stands:
// the header is parsed as the old statement's was, its init and its
// value, so a function literal or a bracket in it is passed over as
// one; then the body, to the matching '}'. The statements after it
// parse and report their own syntax errors. A switch with no body ends
// with its header's line.
func (p *Parser) refuseSwitch() *ast.BadStmt {
	bad := &ast.BadStmt{From: p.pos}
	p.error(p.pos, noSwitch)
	p.next()
	prev := p.exprLev
	p.exprLev = -1
	if p.tok != token.LBRACE && p.tok != token.RBRACE && !p.atLineEnd() {
		p.parseSimpleStmt()
		if p.tok == token.SEMICOLON && !p.atLineEnd() { // the ';' after an init
			p.next()
			if p.tok != token.LBRACE {
				p.parseSimpleStmt()
			}
		}
	}

	p.exprLev = prev
	if p.tok == token.LBRACE {
		p.next()
		p.skipTo(newKindSet()) // to the '}' that closes the body
		if p.tok == token.RBRACE {
			p.next()
		}
	}

	bad.To = p.pos
	return bad
}

// atLineEnd reports whether the token is the semicolon the scanner put
// at the end of a line, or the end of the file.
func (p *Parser) atLineEnd() bool {
	return p.tok == token.EOF || p.tok == token.SEMICOLON && p.lit == "\n"
}

// parseSelectStmt parses "select" "{" {CommClause} "}". Each case is a
// send, a receive, or a receive assigned to variables.
//
//	case out <- r:            SendStmt
//	case <-cancel.Done():     ExprStmt
//	case t := <-tasks:        AssignStmt
//	default:                  Comm nil
func (p *Parser) parseSelectStmt() *ast.SelectStmt {
	s := &ast.SelectStmt{Select: p.expect(token.SELECT)}
	if !p.expectLbrace() {
		return s
	}

	for p.tok == token.CASE || p.tok == token.DEFAULT {
		c := &ast.CommClause{Case: p.pos}
		if p.tok == token.CASE {
			p.next()
			c.Comm = p.parseSimpleStmt()
		} else {
			p.next()
		}

		p.expect(token.COLON)
		c.Body = p.parseStmtList()
		s.Body = append(s.Body, c)
	}

	p.expect(token.RBRACE)
	return s
}
