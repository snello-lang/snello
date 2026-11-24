// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"slices"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// attrToken is the token an attribute begins with, whose node is kept
// beside the syntax tree; ifWord the keyword a match arm's guard begins
// with; superWord the keyword a method calls the next definition with,
// which is parsed as a name; and fieldsFunc and variantsFunc the
// functions that give a type's parts, whose members their comments
// document.
const (
	attrToken    = "@"
	ifWord       = "if"
	superWord    = "super"
	fieldsFunc   = "fields"
	variantsFunc = "variants"
)

// formTokens are the tokens a form the language writes with its own
// syntax is written with, whose node a place on them is looked for at:
// a type's, map[K]V, ref T, T? by its last and a channel's direction,
// <-chan T and chan<- T; an operator's, x?, x ?? y, <-ch, &x, a match
// arm's =>, a variadic's ... and an attribute's @; a pattern's | and
// ..=; and an arithmetic operator, a comparison and a compound
// assignment, which a standard trait is behind. chanTokens are those of
// a channel's type, which begins with either, and patternTokens those
// that join a pattern's parts, which no node holds.
var (
	formTokens    = []string{"map", "chan", "<-", "ref", "mut", "[", "*", "?", "??", "&", "=>", "...", "@", "|", "..=", "+", "-", "/", "%", "==", "!=", "<", "<=", ">", ">=", "+=", "-=", "*=", "/=", "%="}
	chanTokens    = []string{"chan", "<-"}
	patternTokens = []string{"|", "..="}
)

// tokenForm returns the documentation of the form the token text at
// pos is part of: an attribute's, from its @; the node of a form's or
// an operator's token, a channel's type from either of its tokens, a
// pattern from what joins its parts, with the form as the place writes
// it; a keyword's, by the keyword, but a kind an arm of a match on a
// type names, struct or enum, and a guard's if, which are a pattern's;
// false for any other token.
func (v *view) tokenForm(syntax *ast.File, text string, pos token.Pos) (builtinDecl, bool) {
	forms := builtins().forms
	if text == attrToken && isAttr(syntax, pos) {
		d, ok := forms[doc.FormAttribute]
		return d, ok
	}

	if slices.Contains(formTokens, text) {
		n := owner(syntax, pos)
		switch {
		case slices.Contains(chanTokens, text):
			if ch := chanTypeAt(syntax, pos); ch != nil {
				n = ch
			}
		case slices.Contains(patternTokens, text):
			if patternAt(syntax, pos) {
				d, ok := forms[doc.FormPattern]
				return d, ok
			}
		}

		if d, ok := v.formAt(n); ok {
			return d, true
		}
	}

	if !token.Lookup(text).IsKeyword() && text != doc.FormBlank {
		return builtinDecl{}, false
	}

	if _, kind := owner(syntax, pos).(*ast.TypePat); kind || text == ifWord && guardAt(syntax, pos) {
		d, ok := forms[doc.FormPattern]
		return d, ok
	}

	d, ok := forms[text] // a keyword's, or the blank name's, a pattern's _ among them
	return d, ok
}

// formAt returns the documentation of the form the syntax node n
// writes, builtinFormOf's, with the form as the place writes it: a
// type's text; the type of an expression an operator gives, &x or x?;
// and a compound assignment's, the type of what it changes, in place of
// the file's.
func (v *view) formAt(n ast.Node) (builtinDecl, bool) {
	d, ok := builtinFormOf(n)
	if !ok {
		return d, false
	}

	switch n := n.(type) {
	case ast.Type:
		d.decl = ast.SourceType(n)
	case *ast.UnaryExpr, *ast.TryExpr, *ast.CoalesceExpr, *ast.BinaryExpr:
		if t := v.info.Types[n.(ast.Expr)]; t != nil {
			d.decl = t.String()
		}
	case *ast.AssignStmt:
		if t := v.info.Types[n.Lhs[0]]; t != nil {
			d.decl = t.String()
		}
	}

	return d, true
}

// wordForm returns the documentation of a name the language writes
// whose object is not what a reader asks about: super, before a dot,
// which the checker records as a use of the receiver, and the blank
// name, wherever it stands; false for any other name.
func wordForm(syntax *ast.File, id *ast.Ident) (builtinDecl, bool) {
	switch {
	case id.Name == superWord && selectorOf(syntax, id) == nil, id.Name == doc.FormBlank:
		d, ok := builtins().forms[id.Name]
		return d, ok
	}

	return builtinDecl{}, false
}

// nameDoc returns the documentation of a name no object stands for: a
// method of a type the language declares, xs.Push; a member of a part
// of a type, f.default or f.type, by the comment of the function that
// gives the part; and Result in a type, which is in no scope; false
// for any other.
func (v *view) nameDoc(syntax *ast.File, id *ast.Ident) (builtinDecl, bool) {
	if sel := selectorOf(syntax, id); sel != nil {
		if d, ok := builtinMethodOf(v.info.Types[sel.X], id.Name); ok {
			return d, true
		}

		return v.partDoc(sel.X)
	}

	nt := namedTypeOf(syntax, id)
	switch {
	case nt == nil:
		return builtinDecl{}, false
	case nt.Pkg == nil:
		d, ok := builtins().names[id.Name]
		return d, ok
	}

	return v.partDoc(nt.Pkg)
}

// partDoc returns the documentation of a member of the part x names,
// a field fields(T) gives or a variant variants(T) gives: the comment of
// the function that gives the part, which says what each member is.
func (v *view) partDoc(x ast.Node) (builtinDecl, bool) {
	id, ok := x.(*ast.Ident)
	if !ok {
		return builtinDecl{}, false
	}

	part, ok := v.info.Uses[id].(*types.Part)
	if !ok {
		return builtinDecl{}, false
	}

	giver := fieldsFunc
	if part.IsVariant() {
		giver = variantsFunc
	}

	d, ok := builtins().names[giver]
	return d, ok
}

// paramOf returns the parameter, or the receiver, id names in the
// declaration of a function without a body, one the runtime or C
// provides, or a trait's required method, whose parameters the checker
// declares no object for: the one its signature holds; nil for any
// other name. A function's body is not walked.
func (v *view) paramOf(syntax *ast.File, id *ast.Ident) types.Object {
	var found types.Object
	ast.Inspect(syntax, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || found != nil {
			return found == nil
		}

		if fd.Body != nil {
			return false
		}

		if fn, _ := v.info.Defs[fd.Name].(*types.Func); fn != nil {
			if sig, _ := fn.Type().(*types.Signature); sig != nil {
				found = paramIn(fd, sig, id)
			}
		}

		return false
	})

	return found
}

// paramIn returns the receiver or the parameter id names in fd, from
// sig, the function's signature, or nil.
func paramIn(fd *ast.FuncDecl, sig *types.Signature, id *ast.Ident) types.Object {
	if fd.Recv != nil && fd.Recv.Name == id && sig.Recv() != nil {
		return sig.Recv()
	}

	for i, p := range fd.Params {
		if p.Name == id && i < len(sig.Params()) {
			return sig.Params()[i]
		}
	}

	return nil
}

// isAttr reports whether an attribute's @ is at pos, among the
// attributes the syntax keeps beside its tree.
func isAttr(syntax *ast.File, pos token.Pos) bool {
	for _, attrs := range syntax.Attrs {
		for _, a := range attrs {
			if a.At == pos {
				return true
			}
		}
	}

	return false
}

// patternAt reports whether pos is in a match arm's pattern, between
// its start and its =>, before any guard: where a | joins alternatives,
// of values or of types, and a ..= a range's ends. The arm's extent, and
// not the pattern's inner shape, decides: the checker gives each type of
// an arm of a match on a type, i8 | i16, an arm of its own.
func patternAt(syntax *ast.File, pos token.Pos) bool {
	found := false
	ast.Inspect(syntax, func(n ast.Node) bool {
		if arm, ok := n.(*ast.MatchArm); ok && arm.Pat.Pos() <= pos && pos < arm.Arrow {
			found = arm.Guard == nil || pos < arm.Guard.Pos()
		}

		return !found
	})

	return found
}

// guardAt reports whether the if at pos begins a match arm's guard,
// between the arm's pattern and its condition.
func guardAt(syntax *ast.File, pos token.Pos) bool {
	found := false
	ast.Inspect(syntax, func(n ast.Node) bool {
		if arm, ok := n.(*ast.MatchArm); ok && arm.Guard != nil && arm.Pat.Pos() < pos && pos < arm.Guard.Pos() {
			found = true
		}

		return !found
	})

	return found
}

// namedTypeOf returns the named type whose name id is, Result in
// Result<T, E> or type in f.type, or nil for a name no type is written
// with.
func namedTypeOf(syntax *ast.File, id *ast.Ident) *ast.NamedType {
	var found *ast.NamedType
	ast.Inspect(syntax, func(n ast.Node) bool {
		if nt, ok := n.(*ast.NamedType); ok && nt.Name == id {
			found = nt
		}

		return found == nil
	})

	return found
}

// chanTypeAt returns the channel type whose arrow or keyword is at pos,
// <-chan T's chan as well as its arrow, chan<- T's arrow as well as its
// chan, or nil for an arrow that sends or receives.
func chanTypeAt(syntax *ast.File, pos token.Pos) *ast.ChanType {
	var found *ast.ChanType
	ast.Inspect(syntax, func(n ast.Node) bool {
		if ch, ok := n.(*ast.ChanType); ok && ch.Begin <= pos && pos < ch.Elem.Pos() {
			found = ch
		}

		return true
	})

	return found
}
