// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parser

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
)

// typeStart is the set of tokens that can begin a type. Callers test
// it before calling parseType where a type is optional, as in a result
// list, or where a line may hold something else, as in a struct body.
var typeStart = newKindSet(
	token.IDENT, token.SELF, token.MUL, token.REF, token.MUT,
	token.LBRACK, token.MAP, token.CHAN, token.ARROW, token.FUNC,
	token.LPAREN, token.STRUCT,
)

// parseType parses a type expression. The grammar, from loosest to
// tightest binding:
//
//	Type   = Prefix ["?"]
//	Prefix = ("ref" | "mut" | "*") Prefix | Core
//	Core   = Named | "Self" | "[" "]" Type | "[" N "]" Type
//	       | "map" "[" Type "]" Type | ("chan" | "chan<-" | "<-chan") Type
//	       | "func" Params [Results] | "(" Type {"," Type} ")"
//	       | "struct" "{" Fields "}"
//	Named  = ident ["." ident] ["<" Type {"," Type} ">"]
//
// So the postfix "?" applies to the whole pointer or borrow before it
// (*Node? is an optional pointer, ref T? an optional borrow) but inside a
// container it applies to the element ([]int? is a slice of optional
// ints), because a container's element is itself a full Type.
//
//	*Node<T>?      OptionType{PointerType{NamedType}}
//	[]int?         SliceType{OptionType{NamedType}}
//	ref []T        RefType{SliceType{NamedType}}
//
// parseType is the entry point for every type in the language; the
// other functions in this file parse one form each and recurse through
// it for their element types. On a token that cannot start a type it
// reports an error and returns a placeholder, so callers always get a
// node and need not check for nil.
func (p *Parser) parseType() ast.Type {
	typ := p.parsePrefixedType()
	if p.tok == token.QUESTION {
		typ = &ast.OptionType{Elem: typ, Question: p.pos}
		p.next()
	}

	return typ
}

// parsePrefixedType parses the "ref", "mut" and "*" prefixes, as many
// as there are, in front of a core type. The prefixes nest right to
// left, so "ref *Node" is a borrow of a pointer, and the postfix "?" is
// left to parseType so that it applies outside all of them.
//
//	*Node          PointerType{NamedType}
//	mut *Tree      RefType{Mut: true, PointerType{NamedType}}
func (p *Parser) parsePrefixedType() ast.Type {
	switch p.tok {
	case token.REF, token.MUT:
		pos, mut := p.pos, p.tok == token.MUT
		p.next()
		return &ast.RefType{Ref: pos, Mut: mut, Elem: p.parsePrefixedType()}
	case token.MUL:
		pos := p.pos
		p.next()
		return &ast.PointerType{Star: pos, Elem: p.parsePrefixedType()}
	}

	return p.parseCoreType()
}

// parseCoreType dispatches on the current token to the parser for one
// core type form: a name, Self, a slice or array, a map, a channel, a
// function, a tuple or a struct. The map form is short enough to parse
// in place. When the token starts no type at all it reports "expected
// type" and returns a NamedType called "_", so the tree stays complete.
//
//	map[string]Entry    MapType{NamedType, NamedType}
//	Self                SelfType
func (p *Parser) parseCoreType() ast.Type {
	pos := p.pos

	switch p.tok {
	case token.IDENT:
		return p.parseNamedType()
	case token.SELF:
		p.next()
		return &ast.SelfType{Self: pos}
	case token.LBRACK:
		return p.parseSliceOrArrayType()
	case token.MAP:
		p.next()
		p.expect(token.LBRACK)
		key := p.parseType()
		p.expect(token.RBRACK)
		return &ast.MapType{Map: pos, Key: key, Value: p.parseType()}
	case token.CHAN, token.ARROW:
		return p.parseChanType()
	case token.FUNC:
		return p.parseFuncType()
	case token.LPAREN:
		return p.parseTupleType()
	case token.STRUCT:
		return p.parseStructType()
	}

	p.errorExpected(pos, "type")
	return &ast.NamedType{Name: &ast.Ident{NamePos: pos, Name: "_"}}
}

// parseNamedType parses: ident ["." ident] ["<" types ">"]. The first
// identifier is the name until a "." shows it was a package.
//
//	int                 NamedType{Name: int}
//	http.Request        NamedType{Pkg: http, Name: Request}
//	arena.Index<Node>   NamedType{Pkg: arena, Name: Index, Args: [Node]}
func (p *Parser) parseNamedType() *ast.NamedType {
	typ := &ast.NamedType{Name: p.parseIdent()}
	if p.tok == token.PERIOD {
		p.next()
		typ.Pkg, typ.Name = typ.Name, p.parseIdent()
	}

	if p.tok == token.LSS {
		typ.Args = p.parseTypeArgs()
	}

	return typ
}

// parseTypeArgs parses "<" Type {"," Type} ">". In a type, "<" always
// opens a type argument list; the ambiguity with less-than exists only
// in expressions and is resolved there. At least one argument is
// required, so "List<>" is an error. The closing ">" goes through
// expectGtr because the scanner delivers the end of a nested list such
// as Index<entry<K, V>> as a single ">>" token.
//
//	<T>                 [NamedType]
//	<K, Index<V>>       [NamedType, NamedType{Args: [NamedType]}]
func (p *Parser) parseTypeArgs() []ast.Type {
	p.expect(token.LSS)

	var args []ast.Type
	for {
		args = append(args, p.parseType())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	p.expectGtr()
	return args
}

// parseSliceOrArrayType parses "[" "]" Type or "[" N "]" Type, the
// two forms that begin with a bracket. The decision is made by the
// token after the bracket: a "]" means slice, anything else is the
// array length.
//
//	[]*Node        SliceType{PointerType{NamedType}}
//	[16]float64    ArrayType{Len: BasicLit 16, Elem: NamedType}
func (p *Parser) parseSliceOrArrayType() ast.Type {
	pos := p.expect(token.LBRACK)
	if p.tok == token.RBRACK {
		p.next()
		return &ast.SliceType{Lbrack: pos, Elem: p.parseType()}
	}

	// Until expressions arrive, an array length is an integer literal.
	if p.tok != token.INT {
		p.errorExpected(p.pos, "array length")
	}

	length := &ast.BasicLit{ValuePos: p.pos, Kind: token.INT, Value: p.lit}
	p.next()
	p.expect(token.RBRACK)
	return &ast.ArrayType{Lbrack: pos, Len: length, Elem: p.parseType()}
}

// parseChanType parses: "chan" Type | "chan" "<-" Type | "<-" "chan" Type.
// The direction is read from the arrow's side of "chan", as in Go. An
// arrow on both sides is not consumed as a direction; it is left for
// the element type, which reports "expected 'chan'".
//
//	chan Job               ChanType{Dir: SendRecv}
//	chan<- Result<T, E>    ChanType{Dir: SendOnly}
//	<-chan struct{}        ChanType{Dir: RecvOnly}
func (p *Parser) parseChanType() *ast.ChanType {
	typ := &ast.ChanType{Begin: p.pos, Dir: ast.SendRecv}
	if p.tok == token.ARROW {
		p.next()
		typ.Dir = ast.RecvOnly
	}

	p.expect(token.CHAN)
	if p.tok == token.ARROW && typ.Dir == ast.SendRecv {
		p.next()
		typ.Dir = ast.SendOnly
	}

	typ.Elem = p.parseType()
	return typ
}

// parseFuncType parses: "func" Params [Results]. The same function
// serves a function type in a field or parameter and, once declarations
// arrive, the signature of a func declaration after its name.
//
//	func(ref T) bool                  FuncType{Params: [T], Results: [bool]}
//	func(xs ref []T) (List<T>, int)   FuncType{Params: [xs], Results: 2}
func (p *Parser) parseFuncType() *ast.FuncType {
	pos := p.expect(token.FUNC)
	return &ast.FuncType{Func: pos, Params: p.parseParams(), Results: p.parseResults()}
}

// parseParams parses "(" [Param {"," Param}] ")". A parameter is a type
// alone, as in func(T) U, or names followed by a type, as in
// (from, to NodeID, w int). An identifier followed by a comma is held
// back until the parser sees what ends the group: a type makes the held
// identifiers names of that type; a closing parenthesis makes them
// types themselves. This mirrors go/parser, which faces the same
// question with the same syntax.
//
//	(T, U)               two unnamed params: Type T, Type U
//	(from, to NodeID)    two named params, both with Type NodeID
//	(x int, f func(T))   two named params
//
// A group of names shares one Type node, so (from, to NodeID) yields
// two Param values whose Type fields point at the same NamedType.
func (p *Parser) parseParams() []*ast.Param {
	p.expect(token.LPAREN)

	// Identifiers whose role is not yet known. Parameter lists are short,
	// so the buffer keeps them on the stack rather than the heap.
	var buf [4]*ast.Ident
	names := buf[:0]

	var params []*ast.Param
	for p.tok != token.RPAREN && p.tok != token.EOF {
		if p.tok == token.IDENT && p.peek() == token.COMMA {
			names = append(names, p.parseIdent())
			p.next()
			continue
		}

		if p.tok == token.IDENT && typeStart[p.peek()] {
			names = append(names, p.parseIdent())
			typ := p.parseType()
			for _, name := range names {
				params = append(params, &ast.Param{Name: name, Type: typ})
			}
		} else {
			params = appendTypeParams(params, names)
			params = append(params, &ast.Param{Type: p.parseType()})
		}

		names = names[:0]
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	params = appendTypeParams(params, names)
	p.expect(token.RPAREN)
	return params
}

// appendTypeParams adds one unnamed parameter per held-back identifier,
// each identifier being a type name after all. For (T, U) it turns the
// held identifiers T and U into Param{Type: NamedType T} and
// Param{Type: NamedType U}.
func appendTypeParams(params []*ast.Param, names []*ast.Ident) []*ast.Param {
	for _, name := range names {
		params = append(params, &ast.Param{Type: &ast.NamedType{Name: name}})
	}

	return params
}

// parseResults parses an optional result list: one type, or several in
// parentheses. No type at all means no results. Result names are not
// part of Teak, so the parenthesized form is a plain type list.
//
//	func() int               [int]
//	func() (*Node, error)    [PointerType, error]
//	func()                   nil
func (p *Parser) parseResults() []ast.Type {
	if p.tok == token.LPAREN {
		return p.parseTypeList()
	}

	if typeStart[p.tok] {
		return []ast.Type{p.parseType()}
	}

	return nil
}

// parseTupleType parses "(" Type {"," Type} ")". A single parenthesized
// type is just that type, which is how a type is grouped under a "?":
// (ref string, ref string)? is an optional pair, and (A)? is the same
// as A?.
//
//	(A, B)      TupleType{Elems: [A, B]}
//	(A)         NamedType A
func (p *Parser) parseTupleType() ast.Type {
	pos := p.pos
	elems := p.parseTypeList()
	if len(elems) == 1 {
		return elems[0]
	}

	return &ast.TupleType{Lparen: pos, Elems: elems}
}

// parseTypeList parses "(" [Type {"," Type}] ")" and returns the types.
// It is the shared loop behind tuple types and parenthesized results;
// the caller decides what the list means.
//
//	()              nil
//	(int, string)   [int, string]
func (p *Parser) parseTypeList() []ast.Type {
	p.expect(token.LPAREN)

	var types []ast.Type
	for p.tok != token.RPAREN && p.tok != token.EOF {
		types = append(types, p.parseType())
		if p.tok != token.COMMA {
			break
		}

		p.next()
	}

	p.expect(token.RPAREN)
	return types
}

// parseStructType parses "struct" "{" {Field ";"} "}". It is the type
// form used after "type Name struct"; the keyword and the field list are
// the same whether the struct is named or anonymous.
//
//	struct{}                            StructType{Fields: nil}
//	struct { w, h float64; Point }      StructType{Fields: [w h, Point]}
func (p *Parser) parseStructType() *ast.StructType {
	pos := p.expect(token.STRUCT)
	return &ast.StructType{Struct: pos, Fields: p.parseFieldList()}
}

// parseFieldList parses a brace-delimited list of fields: names followed
// by a type, or an embedded type alone, one per line. The scanner's
// semicolon insertion ends each line, so a field is a line.
//
//	{
//		id   int           Field{Names: [id], Type: int}
//		next *Node?        Field{Names: [next], Type: OptionType}
//		Point              Field{Names: nil, Type: NamedType Point}
//	}
//
// On a line that cannot start a field it reports one error and skips to
// the end of the line; on a line with trailing junk after the field it
// does the same. If the skip runs into a declaration keyword, the struct
// was never closed, and the list ends so that the declaration loop can
// pick up where the struct left off.
func (p *Parser) parseFieldList() []*ast.Field {
	if !p.expectLbrace() {
		return nil
	}

	var fields []*ast.Field
	for p.tok != token.RBRACE && p.tok != token.EOF {
		if !typeStart[p.tok] {
			p.errorExpected(p.pos, "field or '}'")
			if !p.skipFieldLine() {
				return fields
			}

			continue
		}

		fields = append(fields, p.parseField())
		if !p.expectSemi() && !p.skipFieldLine() {
			return fields
		}
	}

	p.expect(token.RBRACE)
	return fields
}

// skipFieldLine recovers inside a field list by skipping the rest of
// the line. It reports whether the list continues: false means a
// declaration start was reached, so the struct was never closed and the
// token is left for the declaration loop.
//
//	a int b }       skips "b", stops at "}": the list continues
//	a int b <nl>    skips "b", consumes the newline: the list continues
//	type            already at a declaration keyword: returns false
func (p *Parser) skipFieldLine() bool {
	p.advance(fieldEnd)
	switch p.tok {
	case token.SEMICOLON:
		p.next()
		return true
	case token.RBRACE:
		return true
	}

	return false
}

// parseField parses: ident {"," ident} Type | Type. As in parseParams,
// an identifier is a name only if a comma or a type follows it; an
// identifier followed by the end of the line is an embedded type.
//
//	id int          Field{Names: [id], Type: int}
//	w, h float64    Field{Names: [w, h], Type: float64}
//	Point           Field{Names: nil, Type: Point}
//	pkg.Base        Field{Names: nil, Type: NamedType{Pkg: pkg}}
func (p *Parser) parseField() *ast.Field {
	field := &ast.Field{}
	if p.tok == token.IDENT && (p.peek() == token.COMMA || typeStart[p.peek()]) {
		for {
			field.Names = append(field.Names, p.parseIdent())
			if p.tok != token.COMMA {
				break
			}

			p.next()
		}
	}

	field.Type = p.parseType()
	return field
}
