// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package parser builds the syntax tree of a Teak source file, modeled
// on go/parser.
//
// The parser is recursive descent with one token of lookahead: it holds
// the current token and asks the scanner for the next when it has
// consumed it. On a syntax error it records the error, skips to the
// next token that can start a declaration, and continues, so one
// mistake produces one message rather than a cascade.
//
// Every loop that recovers from an error must make progress on each
// pass: after skipping to a recovery token, it either consumes that
// token or leaves the loop. A loop that neither consumes nor leaves
// spins forever on the first token it cannot handle.
package parser

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/scanner"
	"github.com/teak-lang/teak/token"
)

// Recovery sets: after an error the parser skips to a token in the set
// that fits where it was. declStart is the tokens that can begin a
// top-level declaration; groupEnd adds the parenthesis that closes an
// import group, so an error inside the group does not skip past it.
var (
	declStart = newKindSet(token.IMPORT, token.FUNC, token.TYPE, token.TRAIT, token.VAR, token.CONST)
	groupEnd  = declStart.with(token.RPAREN)
	fieldEnd  = declStart.with(token.SEMICOLON, token.RBRACE)

	// stmtStart is the set of keywords that begin a statement and
	// cannot be part of a broken one, so recovery inside a block may
	// resume at any of them. stmtEnd adds the tokens that end one.
	stmtStart = newKindSet(token.BREAK, token.CONST, token.CONTINUE, token.DEFER, token.FOR, token.SPAWN,
		token.IF, token.RETURN, token.SELECT, token.SWITCH, token.VAR)
	stmtEnd = stmtStart.with(token.SEMICOLON, token.RBRACE, token.CASE, token.DEFAULT)
)

// A kindSet is a set of token kinds as a table indexed by Kind: a
// membership test is one array load, with no hashing and no allocation.
type kindSet [token.NumKinds]bool

// A Parser holds the state of one parse of one file.
type Parser struct {
	file    *token.File
	scanner scanner.Scanner
	errors  scanner.ErrorList

	// The current token: the one the parser is looking at and has not
	// yet consumed. next() replaces it with the one after.
	pos token.Pos
	tok token.Kind
	lit string

	// One token of lookahead beyond the current one, filled by peek()
	// and drained by next(). Only a few decisions need it, such as
	// telling a parameter name from a type name.
	ahead lookahead

	// exprLev is the expression nesting level, as in go/parser: it
	// counts open parentheses and brackets, and is -1 inside a control
	// clause, where a '{' opens the body rather than a composite
	// literal: `if x == y {` must not read `y {` as a literal.
	exprLev int
}

// A lookahead is one scanned token held for peek.
type lookahead struct {
	valid bool
	pos   token.Pos
	tok   token.Kind
	lit   string
}

// A checkpoint is the parser's place in the token stream, saved before
// a speculative read and restored after it. The scanner holds its state
// in plain fields, so saving is a struct copy and restoring is another;
// neither allocates.
type checkpoint struct {
	scanner scanner.Scanner
	pos     token.Pos
	tok     token.Kind
	lit     string
	ahead   lookahead
}

// ParseFile parses src as a Teak source file. It always returns a File,
// possibly partial, and the errors found, sorted by position. The
// returned token.File resolves positions in the tree.
func ParseFile(filename string, src []byte) (*ast.File, *token.File, error) {
	var p Parser
	p.init(filename, src)
	f := p.parseFile()
	p.errors.Sort()

	return f, p.file, p.errors.Err()
}

func (p *Parser) init(filename string, src []byte) {
	p.file = token.NewFile(filename, len(src))
	p.scanner.Init(p.file, src, func(pos token.Position, msg string) {
		p.errors.Add(pos, msg)
	})
	p.next()
}

// next advances to the following token.
func (p *Parser) next() {
	if p.ahead.valid {
		p.pos, p.tok, p.lit = p.ahead.pos, p.ahead.tok, p.ahead.lit
		p.ahead.valid = false
		return
	}

	p.pos, p.tok, p.lit = p.scanner.Scan()
}

// save returns a checkpoint of the current position.
func (p *Parser) save() checkpoint {
	return checkpoint{p.scanner, p.pos, p.tok, p.lit, p.ahead}
}

// restore returns the parser to a saved checkpoint.
func (p *Parser) restore(c checkpoint) {
	p.scanner, p.pos, p.tok, p.lit, p.ahead = c.scanner, c.pos, c.tok, c.lit, c.ahead
}

// peek returns the kind of the token after the current one without
// consuming anything.
func (p *Parser) peek() token.Kind {
	if !p.ahead.valid {
		p.ahead.pos, p.ahead.tok, p.ahead.lit = p.scanner.Scan()
		p.ahead.valid = true
	}

	return p.ahead.tok
}

// expectGtr consumes the '>' that closes a type argument list. The
// scanner cannot know that the '>>' in Arc<RwLock<T>> is two closing
// brackets rather than a shift, so the parser splits it here: it
// consumes one '>' and leaves a '>' as the current token.
func (p *Parser) expectGtr() {
	switch p.tok {
	case token.GTR:
		p.next()
	case token.SHR:
		p.tok = token.GTR
		p.pos++
	case token.GEQ:
		p.tok = token.ASSIGN
		p.pos++
	case token.SHR_ASSIGN:
		p.tok = token.GEQ
		p.pos++
	default:
		p.errorExpected(p.pos, "'>'")
	}
}

func (p *Parser) error(pos token.Pos, msg string) {
	p.errors.Add(p.file.Position(pos), msg)
}

// errorExpected reports what the parser wanted and what it found, in
// the form "expected 'package', found 'func'".
func (p *Parser) errorExpected(pos token.Pos, what string) {
	found := p.tok.String()
	switch {
	case p.tok == token.SEMICOLON && p.lit == "\n":
		found = "newline"
	case p.tok.IsLiteral():
		found = p.lit
	}

	p.error(pos, "expected "+what+", found '"+found+"'")
}

// expect consumes the current token if it is tok, else reports an error.
// It returns the token's position either way.
func (p *Parser) expect(tok token.Kind) token.Pos {
	pos := p.pos
	if p.tok != tok {
		p.errorExpected(pos, "'"+tok.String()+"'")
	}

	p.next()
	return pos
}

// expectLbrace consumes the '{' that opens a body and reports whether
// it was there. A missing brace is reported once and the body is left
// unparsed, so the caller resumes at whatever comes next instead of
// also reporting the missing '}'.
func (p *Parser) expectLbrace() bool {
	if p.tok != token.LBRACE {
		p.errorExpected(p.pos, "'{'")
		return false
	}

	p.next()
	return true
}

// expectSemi consumes a statement-ending semicolon and reports whether
// it found one. A closing brace or parenthesis also ends a statement, as
// in Go, and is not consumed. The caller decides how to recover.
func (p *Parser) expectSemi() bool {
	switch p.tok {
	case token.SEMICOLON:
		p.next()
		return true
	case token.RPAREN, token.RBRACE, token.EOF:
		return true
	}

	p.errorExpected(p.pos, "';' or newline")
	return false
}

// advance skips tokens until one in the set, or the end of the file.
func (p *Parser) advance(to *kindSet) {
	for p.tok != token.EOF && !to[p.tok] {
		p.next()
	}
}

// parseIdent parses an identifier. On any other token it reports the
// error, leaves the token in place, and returns a placeholder so the
// tree stays well formed.
func (p *Parser) parseIdent() *ast.Ident {
	ident := &ast.Ident{NamePos: p.pos, Name: "_"}
	if p.tok != token.IDENT {
		p.errorExpected(p.pos, "identifier")
		return ident
	}

	ident.Name = p.lit
	p.next()
	return ident
}

// parseFile parses: package clause, imports, declarations.
func (p *Parser) parseFile() *ast.File {
	f := &ast.File{Package: p.expect(token.PACKAGE)}
	f.Name = p.parseIdent()
	if !p.expectSemi() {
		p.advance(declStart)
	}

	for p.tok == token.IMPORT {
		f.Imports = append(f.Imports, p.parseImportDecl()...)
	}

	for p.tok != token.EOF {
		if d := p.parseDecl(); d != nil {
			f.Decls = append(f.Decls, d)
		}
	}

	return f
}

// parseImportDecl parses a single import or a parenthesized group.
func (p *Parser) parseImportDecl() []*ast.ImportSpec {
	p.expect(token.IMPORT)

	var specs []*ast.ImportSpec
	if p.tok != token.LPAREN {
		spec, ok := p.parseImportSpec()
		specs = append(specs, spec)
		if !ok || !p.expectSemi() {
			p.advance(declStart)
		}

		return specs
	}

	p.next()
	for p.tok != token.RPAREN && p.tok != token.EOF {
		spec, ok := p.parseImportSpec()
		specs = append(specs, spec)
		if !ok || !p.expectSemi() {
			// Skip to the closing parenthesis. If a declaration start
			// comes first, the group was never closed: report that once
			// and leave the token for the declaration loop.
			p.advance(groupEnd)
			if p.tok != token.RPAREN {
				p.errorExpected(p.pos, "')'")
				return specs
			}
		}
	}

	if p.tok == token.EOF {
		p.errorExpected(p.pos, "')'")
		return specs
	}

	p.next() // the ')'
	if !p.expectSemi() {
		p.advance(declStart)
	}

	return specs
}

// parseImportSpec parses one import. It reports whether the spec was
// well formed; on a malformed one the caller decides how to recover.
func (p *Parser) parseImportSpec() (*ast.ImportSpec, bool) {
	spec := &ast.ImportSpec{}
	if p.tok == token.IDENT {
		spec.Name = p.parseIdent()
	}

	spec.Path = &ast.BasicLit{ValuePos: p.pos, Kind: token.STRING, Value: `""`}
	if p.tok != token.STRING {
		p.errorExpected(p.pos, "import path")
		return spec, false
	}

	spec.Path.Value = p.lit
	p.next()
	return spec, true
}

func newKindSet(kinds ...token.Kind) *kindSet {
	var s kindSet
	for _, k := range kinds {
		s[k] = true
	}

	return &s
}

// with returns a copy of the set with more kinds added.
func (s *kindSet) with(kinds ...token.Kind) *kindSet {
	out := *s
	for _, k := range kinds {
		out[k] = true
	}

	return &out
}
