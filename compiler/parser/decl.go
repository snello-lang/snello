// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/token"
)

// parseDecl parses one top-level declaration and the semicolon that
// ends it. On a token that starts no declaration it reports the error,
// consumes the token and skips to the next declaration start, so the
// loop in parseFile always makes progress. It returns nil in that case.
//
//	func main() { ... }                FuncDecl
//	struct Circle impl Shape { }       TypeDecl with a StructType
//	enum Expr { }                      TypeDecl with an EnumType
//	type NodeID int                    TypeDecl with any other type
//	trait Shape { ... }                TraitDecl
//	var limit = 10                     VarDecl
func (p *Parser) parseDecl() ast.Decl {
	var d ast.Decl
	switch p.tok {
	case token.FUNC:
		d = p.parseFuncDecl()
	case token.STRUCT, token.ENUM:
		d = p.parseStructOrEnumDecl()
	case token.TYPE:
		d = p.parseTypeDecl()
	case token.TRAIT:
		d = p.parseTraitDecl()
	case token.VAR, token.CONST:
		d = p.parseVarDecl()
	default:
		if p.tok == token.IMPORT {
			p.error(p.pos, "imports must appear before other declarations")
		} else {
			p.errorExpected(p.pos, "declaration")
		}

		p.next()
		p.advance(declStart)
		return nil
	}

	if !p.expectSemi() {
		p.advance(declStart)
	}

	return d
}

// parseFuncDecl parses: "func" [Receiver] ident [TypeParams] Params
// [Results] [Body]. The same function parses a method inside a trait,
// where the body is optional: a method without one is required of
// every implementor. The parser accepts a missing body everywhere and
// leaves it to the checker to reject outside a trait.
//
//	func main() { }                             Recv nil, Body set
//	func (t mut Tree) Push(v T) { }             Recv t: RefType{Mut, Tree}
//	func Map<T, U>(l List<T>, f func(T) U) ...  TypeParams [T, U]
//	func (s ref Self) Area() float64            Body nil (in a trait)
func (p *Parser) parseFuncDecl() *ast.FuncDecl {
	d := &ast.FuncDecl{Func: p.expect(token.FUNC)}
	if p.tok == token.LPAREN {
		d.Recv = p.parseReceiver()
	}

	d.Name = p.parseIdent()
	if p.tok == token.LSS {
		d.TypeParams = p.parseTypeParams()
	}

	d.Params = p.parseParams()
	d.Results = p.parseResults()
	if p.tok == token.LBRACE {
		d.Body = p.parseBlock()
	}

	return d
}

// parseReceiver parses "(" ident Type ")". The type is one of the three
// receiver modes: T consumes, ref T borrows, mut T borrows mutably. Go
// allows an unnamed receiver, (Tree), and Teak does not; that mistake
// gets one message of its own rather than a complaint about the ')'.
//
//	(f File)         Param{f, NamedType File}
//	(t ref Tree)     Param{t, RefType{Tree}}
//	(c mut Cache<K, V>)   Param{c, RefType{Mut, NamedType Cache<K, V>}}
func (p *Parser) parseReceiver() *ast.Param {
	pos := p.expect(token.LPAREN)
	if p.tok == token.RPAREN || p.tok == token.IDENT && p.peek() == token.RPAREN {
		p.error(pos, "receiver needs a name and a type: (t ref Tree)")
		recv := &ast.Param{Name: &ast.Ident{NamePos: p.pos, Name: "_"}}
		if p.tok == token.IDENT {
			recv.Name = p.parseIdent()
		}

		recv.Type = &ast.NamedType{Name: recv.Name}
		p.next() // the ')'
		return recv
	}

	recv := &ast.Param{Name: p.parseIdent()}
	recv.Type = p.parseType()
	p.expect(token.RPAREN)
	return recv
}

// parseTypeParams parses "<" TypeParam {"," TypeParam} ">" where
// TypeParam = ident [":" Bound {"+" Bound}] and a Bound is a trait name.
// The closing ">" goes through expectGtr because a bound may itself end
// in ">", as in <I: Iterator<T>>.
//
//	<T>                        [T]
//	<K: Hash + Eq + Clone, V>  [K with three bounds, V]
func (p *Parser) parseTypeParams() []*ast.TypeParam {
	p.expect(token.LSS)

	var params []*ast.TypeParam
	for {
		tp := &ast.TypeParam{Name: p.parseIdent()}
		if p.tok == token.COLON {
			p.next()
			for {
				tp.Bounds = append(tp.Bounds, p.parseNamedType())
				if p.tok != token.ADD {
					break
				}

				p.next()
			}
		}

		params = append(params, tp)
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	p.expectGtr()
	return params
}

// parseStructOrEnumDecl parses: ("struct" | "enum") ident [TypeParams]
// [Impls] Body, where the body is a field list for a struct and a
// variant list for an enum. The keyword comes first, as with trait and
// func, so a declaration says what it declares in its first word; Go's
// `type Circle struct` is the one piece of Go's declaration syntax Teak
// changes, because the impl list belongs on the thing being declared.
//
//	struct Circle impl Shape { r f64 }     Impls [Shape], Def StructType
//	enum Expr { Lit(f64); Neg(*Expr) }     Def EnumType
//	struct Cache<K: Hash, V> { }           TypeParams [K, V]
func (p *Parser) parseStructOrEnumDecl() *ast.TypeDecl {
	kind, pos := p.tok, p.pos
	p.next()
	d := &ast.TypeDecl{TokPos: pos, Name: p.parseIdent()}
	if p.tok == token.LSS {
		d.TypeParams = p.parseTypeParams()
	}

	d.Impls = p.parseImpls()
	d.Def = p.parseStructOrEnumBody(kind, pos)
	return d
}

// parseStructOrEnumBody parses the body of a struct or enum
// declaration whose keyword, at pos, was already read.
func (p *Parser) parseStructOrEnumBody(kind token.Kind, pos token.Pos) ast.Type {
	if kind == token.STRUCT {
		return &ast.StructType{Struct: pos, Fields: p.parseFieldList()}
	}

	return &ast.EnumType{Enum: pos, Variants: p.parseVariants()}
}

// parseTypeDecl parses: "type" ident [TypeParams] Type [Impls], a new
// type over an existing one, which lists the traits it implements
// after the type it is declared over, as a struct lists them before
// its body. Go's spelling of a struct or enum declaration, `type
// Circle struct { ... }`, is reported once with the Teak spelling and
// then parsed as that declaration, so the tree is still whole.
//
//	type NodeID int                          Def NamedType
//	type Handler func(Request)               Def FuncType
//	type Duration i64 impl fmt.Stringer      Def NamedType, Impls [fmt.Stringer]
func (p *Parser) parseTypeDecl() *ast.TypeDecl {
	d := &ast.TypeDecl{TokPos: p.expect(token.TYPE)}
	d.Name = p.parseIdent()
	if p.tok == token.LSS {
		d.TypeParams = p.parseTypeParams()
	}

	if p.tok == token.IMPL || p.tok == token.STRUCT || p.tok == token.ENUM {
		p.error(d.TokPos, "a struct or enum declaration is spelled 'struct "+d.Name.Name+" {' or 'enum "+d.Name.Name+" {'")
		d.Impls = p.parseImpls()
		if p.tok != token.STRUCT && p.tok != token.ENUM {
			return d
		}

		kind, pos := p.tok, p.pos
		p.next()
		if impls := p.parseImpls(); impls != nil {
			d.Impls = impls
		}

		d.Def = p.parseStructOrEnumBody(kind, pos)
		return d
	}

	d.Def = p.parseType()
	d.Impls = p.parseImpls()
	return d
}

// parseImpls parses an optional "impl" Trait {"," Trait} list and
// returns nil when there is none.
//
//	impl Shape, Named     [Shape, Named]
//	impl Iterator<U>      [NamedType Iterator with Args [U]]
func (p *Parser) parseImpls() []ast.Type {
	if p.tok != token.IMPL {
		return nil
	}

	p.next()
	return p.parseTraitList()
}

// parseTraitList parses Trait {"," Trait}, the shape of both an impl
// list and the refinement list after a trait name. A trait is named,
// never a pointer or a container, so each entry is a NamedType.
func (p *Parser) parseTraitList() []ast.Type {
	var traits []ast.Type
	for {
		traits = append(traits, p.parseNamedType())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	return traits
}

// parseVariants parses "{" {Variant ";"} "}" where Variant = ident
// ["(" Params ")"]. A variant's fields have the grammar of a parameter
// list, positional or named, so parseParams reads them.
//
//	{
//		Null                         Variant{Null}
//		Number(float64)              one unnamed field
//		Bin(op Token, lhs *Expr)     two named fields
//	}
//
// A variant name followed by a type on the same line, as in "Lit
// float64", is the one mistake worth a specific message, since it is
// how a struct field would be written.
func (p *Parser) parseVariants() []*ast.Variant {
	if !p.expectLbrace() {
		return nil
	}

	var variants []*ast.Variant
	for p.tok != token.RBRACE && p.tok != token.EOF {
		if p.tok != token.IDENT {
			p.errorExpected(p.pos, "variant or '}'")
			if !p.skipFieldLine() {
				return variants
			}

			continue
		}

		v := &ast.Variant{Name: p.parseIdent()}
		variants = append(variants, v)
		if p.tok == token.LPAREN {
			v.Fields = p.parseParams()
		} else if typeStart[p.tok] {
			p.errorExpected(p.pos, "'(' or newline")
			if !p.skipFieldLine() {
				return variants
			}

			continue
		}

		if !p.expectSemi() && !p.skipFieldLine() {
			return variants
		}
	}

	p.expect(token.RBRACE)
	return variants
}

// parseTraitDecl parses: "trait" ident [TypeParams] [":" Traits] "{"
// {Member ";"} "}" where a Member is "var" Field, a field every
// implementor gets, or a method: a FuncDecl whose receiver is Self,
// required when it has no body and a default otherwise.
//
//	trait Shape {
//		func (s ref Self) Area() float64             Methods[0], Body nil
//		func (s ref Self) Describe() string { ... }  Methods[1], Body set
//	}
//	trait Named { var name string }                  Fields [name]
//	trait Logged: Shape { }                          Refines [Shape]
func (p *Parser) parseTraitDecl() *ast.TraitDecl {
	d := &ast.TraitDecl{Trait: p.expect(token.TRAIT)}
	d.Name = p.parseIdent()
	if p.tok == token.LSS {
		d.TypeParams = p.parseTypeParams()
	}

	if p.tok == token.COLON {
		p.next()
		d.Refines = p.parseTraitList()
	}

	if !p.expectLbrace() {
		return d
	}

	for p.tok != token.RBRACE && p.tok != token.EOF {
		switch p.tok {
		case token.VAR:
			p.next()
			d.Fields = append(d.Fields, p.parseField())
		case token.FUNC:
			d.Methods = append(d.Methods, p.parseFuncDecl())
		default:
			p.errorExpected(p.pos, "method, field or '}'")
			if !p.skipTraitLine() {
				return d
			}

			continue
		}

		if !p.expectSemi() && !p.skipTraitLine() {
			return d
		}
	}

	p.expect(token.RBRACE)
	return d
}

// skipTraitLine is skipFieldLine for a trait body, where a line may
// also begin with func or var: those two declaration keywords resume
// the body instead of ending it.
func (p *Parser) skipTraitLine() bool {
	return p.skipFieldLine() || p.tok == token.FUNC || p.tok == token.VAR
}

// parseVarDecl parses: ("var" | "const") ident {"," ident} [Type]
// ["=" Expr {"," Expr}]. At least one of the type and the initializer
// must be present, as in Go. The same function serves a declaration
// statement inside a block.
//
//	var x int             Names [x], Type int, no Values
//	var x, y = 1, 2       Names [x, y], Type nil, two Values
//	const limit = 1 << 10 Const true
func (p *Parser) parseVarDecl() *ast.VarDecl {
	d := &ast.VarDecl{TokPos: p.pos, Const: p.tok == token.CONST}
	p.next()
	for {
		d.Names = append(d.Names, p.parseIdent())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	if typeStart[p.tok] {
		d.Type = p.parseType()
	}

	if p.tok == token.ASSIGN {
		p.next()
		for {
			d.Values = append(d.Values, p.parseExpr())
			if p.tok != token.COMMA {
				break
			}

			p.next()
		}
	}

	if d.Type == nil && len(d.Values) == 0 {
		p.errorExpected(p.pos, "type or '='")
	}

	return d
}
