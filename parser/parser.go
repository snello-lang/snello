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
	p.pos, p.tok, p.lit = p.scanner.Scan()
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

func (p *Parser) parseIdent() *ast.Ident {
	name := "_"
	pos := p.pos
	if p.tok == token.IDENT {
		name = p.lit
		p.next()
	} else {
		p.expect(token.IDENT)
	}

	return &ast.Ident{NamePos: pos, Name: name}
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
	if p.tok == token.LPAREN {
		p.next()
		for p.tok != token.RPAREN && p.tok != token.EOF {
			specs = append(specs, p.parseImportSpec())
			if !p.expectSemi() {
				p.advance(groupEnd)
			}
		}

		p.expect(token.RPAREN)
	} else {
		specs = append(specs, p.parseImportSpec())
	}

	if !p.expectSemi() {
		p.advance(declStart)
	}

	return specs
}

func (p *Parser) parseImportSpec() *ast.ImportSpec {
	spec := &ast.ImportSpec{}
	if p.tok == token.IDENT {
		spec.Name = p.parseIdent()
	}

	spec.Path = &ast.BasicLit{ValuePos: p.pos, Kind: token.STRING, Value: `""`}
	if p.tok == token.STRING {
		spec.Path.Value = p.lit
		p.next()
	} else {
		p.errorExpected(p.pos, "import path")
		p.advance(groupEnd)
	}

	return spec
}

// parseDecl parses one top-level declaration. The declaration forms
// arrive with later tasks; until then any declaration is an error, and
// the parser skips to the next one.
func (p *Parser) parseDecl() ast.Decl {
	switch p.tok {
	case token.IMPORT:
		p.error(p.pos, "imports must appear before other declarations")
	default:
		p.errorExpected(p.pos, "declaration")
	}

	p.next()
	p.advance(declStart)
	return nil
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
