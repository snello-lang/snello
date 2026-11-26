// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"

	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The places of a body and the messages of the move pass. A value goes
// to a new owner when it is assigned, passed by value, returned, sent,
// put into a literal or captured by a closure that consumes it; after
// that the old name is dead, which the move pass over the body's form
// decides (formmoves.go). A Copy value is never moved. What is here is
// what both the typing and the passes name: a place, how it is reached,
// and the words of each refusal.

// A place is a variable, or a field path inside one: n, n.value.
type place struct {
	v    *Var
	path string // the field names after the variable, joined by dots
}

// A placeKind says how a place is reached: owned, through a borrow, or
// through an index, which lends the element.
type placeKind int

const (
	notPlace placeKind = iota
	placeOwned
	placeBorrowed
	placeIndexed
)

func (p place) text() string {
	if p.path == "" {
		return p.v.name
	}

	return p.v.name + "." + p.path
}

// within reports whether p is q or lies inside q.
func (p place) within(q place) bool { return p.v == q.v && pathWithin(p.path, q.path) }

// The messages of the move pass, which the pass over the tree and the
// pass over the form both give, so that they say the same thing.

func movedMessage(text, movedAt string) string {
	return "use of moved value " + text + "; moved at " + movedAt
}

func partiallyMovedMessage(text, moved, movedAt string) string {
	return "use of partially moved value " + text + "; " + moved + " moved at " + movedAt
}

func outOfBorrowMessage(text string) string { return "cannot move " + text + " out of a borrow" }

func outOfTemporaryMessage(e ast.Expr) string {
	return "cannot move " + exprText(e) + " out of a temporary: bind the temporary to a name first"
}

func outOfContainerMessage(e ast.Expr, container string) string {
	return "cannot move " + exprText(e) + " out of the " + container
}

func partMovedMessage(e ast.Expr, part *ast.PartExpr) string {
	return "cannot move " + exprText(e) + " out of " + exprText(part.X) + ": a field a loop over a type's parts reaches is lent, since the type may implement Drop, which reads its fields; lend it with ref, or clone it"
}

func dropHolderMessage(e ast.Expr, holder string, t Type) string {
	return "cannot move " + exprText(e) + " out of " + holder + ": " + t.String() + " implements Drop, which reads its fields when " + holder + " goes; clone it, or take it with replace(mut " + exprText(e) + ", value), which leaves value in its place"
}

func loopMovedMessage(text string) string { return text + " moved in a previous iteration of the loop" }

// resolve finds the object a name in an expression already checked
// denotes: in the current scope, or, while the bindings of a match arm
// or an if-binding are checked against the value they take apart, in
// the scope the value was written in, since a binding may reuse a name
// the value reads, if o := o, and the move or borrow of the value is
// the old o's.
func (c *Checker) resolve(name string) Object {
	s := c.scope
	if c.names != nil {
		s = c.names
	}

	_, obj := s.LookupParent(name)
	return obj
}

// refersTo is the object the name id refers to: the typing's record of
// the name, once the typing has resolved it, since the scopes are closed
// by the time the lowering and the passes over the form read it; the
// scope before that.
func (c *Checker) refersTo(id *ast.Ident) Object {
	if obj, ok := c.info.Uses[id]; ok {
		return obj
	}

	return c.resolve(id.Name)
}

// placeOf resolves an expression to the place it denotes and how it is
// reached. Names are looked up again rather than carried on operands,
// so that nothing is allocated per expression on the way.
func (c *Checker) placeOf(e ast.Expr) (place, placeKind) {
	switch e := e.(type) {
	case *ast.Ident:
		if e.Name == "super" { // the receiver of the method being checked (superCall)
			if c.recv == nil {
				return place{}, notPlace
			}

			return place{c.recv, ""}, placeOwned
		}

		v, ok := c.refersTo(e).(*Var)

		if !ok || v.field || v.pos == token.NoPos {
			return place{}, notPlace
		}

		return place{v, ""}, placeOwned // the variable itself, even when its value is a borrow
	case *ast.ParenExpr:
		return c.placeOf(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.MUL {
			return through(c.placeOf(e.X))
		}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok { // a package variable of another package, os.Args: a place, so that the rules of a package variable reach it
			if pkg, ok := c.resolve(id.Name).(*PkgName); ok && pkg.pkg != nil {
				if v, ok := pkg.pkg.scope.Lookup(e.Sel.Name).(*Var); ok && v.global {
					return place{v, ""}, placeOwned
				}

				return place{}, notPlace
			}
		}

		p, kind := through(c.placeOf(e.X))
		if kind == placeOwned || kind == placeBorrowed {
			if p.path == "" {
				p.path = e.Sel.Name
			} else {
				p.path += "." + e.Sel.Name
			}
		}

		return p, kind
	case *ast.IndexExpr:
		p, kind := c.placeOf(e.X)
		if kind == notPlace {
			return p, notPlace
		}

		return p, placeIndexed
	case *ast.SliceExpr:
		p, kind := c.placeOf(e.X) // a view of the place sliced: ref xs[0:2] borrows xs
		if kind == notPlace {
			return p, notPlace
		}

		return p, placeIndexed
	case *ast.PartExpr:
		p, kind := through(c.placeOf(e.X)) // v.[f], a field: a part of v, named [f] in its path
		if kind == placeOwned || kind == placeBorrowed {
			if p.path == "" {
				p.path = "[" + e.Part.Name + "]"
			} else {
				p.path += ".[" + e.Part.Name + "]"
			}
		}

		return p, kind
	}

	return place{}, notPlace
}

// through says how what a place holds is reached: through a borrow
// when the place is a variable holding one.
func through(p place, kind placeKind) (place, placeKind) {
	if kind == placeOwned && p.path == "" {
		if _, ok := p.v.typ.(*Ref); ok {
			return p, placeBorrowed
		}
	}

	return p, kind
}

// isPlaceChain reports whether e reaches a place through selectors,
// indexes, parentheses and dereferences only, so that a selector on it
// checks the whole chain as one use.
func isPlaceChain(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return true
	case *ast.ParenExpr:
		return isPlaceChain(e.X)
	case *ast.SelectorExpr:
		return isPlaceChain(e.X)
	case *ast.IndexExpr:
		return isPlaceChain(e.X)
	case *ast.UnaryExpr:
		return e.Op == token.MUL && isPlaceChain(e.X)
	}

	return false
}

// operandOf evaluates an expression that is read for its value inside a
// place, such as the pointer *p reaches through, with the flags of the
// place around it off.
func (c *Checker) operandOf(e ast.Expr, hint Type) operand {
	inner, noUse := c.inner, c.noUse
	c.inner, c.noUse = false, false
	op := c.expr(e, hint)
	c.inner, c.noUse = inner, noUse
	return op
}

func isMut(t Type) bool {
	r, ok := t.(*Ref)
	return ok && r.mut
}

func isBorrow(t Type) bool {
	_, ok := t.(*Ref)
	return ok
}

// dropHolder returns the innermost whole the place p is a field of
// whose type implements Drop, as text, and that type; "" when there is
// none. Such a whole's Drop reads its fields when it goes, so none of
// them may leave it first. The wholes are the variable and each field
// along p's path, before the last.
func dropHolder(p place) (string, Type) {
	if p.path == "" {
		return "", nil
	}

	holder, at := -1, 0 // the bytes of p.path naming the whole found, and the whole whose field is next; the text is built only for one found
	var held Type
	t := p.v.typ
	for rest := p.path; ; {
		name, tail, more := strings.Cut(rest, ".")
		t = pointee(t)
		if implements(t, DropTrait) {
			holder, held = at, t
		}

		st, ok := t.Underlying().(*Struct)
		if !ok {
			break
		}

		f := fieldNamed(st.fields, name)
		if f == nil || !more {
			break
		}

		t, rest = f.typ, tail
		if at > 0 {
			at++ // the dot before name
		}

		at += len(name)
	}

	switch holder {
	case -1:
		return "", nil
	case 0:
		return p.v.name, held
	}

	return p.v.name + "." + p.path[:holder], held
}

// pointee returns what a borrow or an owned pointer t reaches, or t.
func pointee(t Type) Type {
	for {
		switch u := t.(type) {
		case *Ref:
			t = u.elem
		case *Pointer:
			t = u.elem
		default:
			return t
		}
	}
}

// containerName names what an index reaches into, for a message.
func (c *Checker) containerName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.IndexExpr:
			if _, isMap := c.placeType(x.X).Underlying().(*Map); isMap {
				return "map"
			}

			return "slice"
		case *ast.SelectorExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return "slice"
		}
	}
}

// placeType is the type of the value a place expression reaches, found
// through the variable's type and the fields' types.
func (c *Checker) placeType(e ast.Expr) Type {
	switch e := e.(type) {
	case *ast.Ident:
		if obj := c.resolve(e.Name); obj != nil && obj.Type() != nil {
			return obj.Type()
		}
	case *ast.ParenExpr:
		return c.placeType(e.X)
	case *ast.UnaryExpr:
		return stripRef(c.placeType(e.X))
	case *ast.SelectorExpr:
		if st, ok := stripRef(c.placeType(e.X)).Underlying().(*Struct); ok {
			if f := fieldNamed(st.fields, e.Sel.Name); f != nil {
				return f.typ
			}
		}
	case *ast.IndexExpr:
		switch t := stripRef(c.placeType(e.X)).Underlying().(type) {
		case *Slice:
			return t.elem
		case *Map:
			return t.value
		}
	}

	return Typ[Invalid]
}

// stripRef is the type behind the borrows and owned pointers in front
// of t.
func stripRef(t Type) Type {
	for {
		switch u := t.(type) {
		case *Ref:
			t = u.elem
		case *Pointer:
			t = u.elem
		default:
			return t
		}
	}
}

// exprText writes a place expression as the source would, for a
// message: xs[0], n.value.
func exprText(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.ParenExpr:
		return "(" + exprText(e.X) + ")"
	case *ast.SpawnExpr:
		return "spawn " + exprText(e.Call)
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.PartExpr:
		return exprText(e.X) + ".[" + e.Part.Name + "]"
	case *ast.IndexExpr:
		return exprText(e.X) + "[" + exprText(e.Index) + "]"
	case *ast.SliceExpr:
		return exprText(e.X) + "[" + optText(e.Low) + ":" + optText(e.High) + "]"
	case *ast.UnaryExpr:
		if e.Op == token.REF || e.Op == token.MUT {
			return e.Op.String() + " " + exprText(e.X) // the keyword, then the operand: ref fs
		}

		return e.Op.String() + exprText(e.X)
	case *ast.BinaryExpr:
		return exprText(e.X) + " " + e.Op.String() + " " + exprText(e.Y)
	case *ast.CallExpr:
		if len(e.Args) == 0 {
			return exprText(e.Fun) + "()"
		}

		return exprText(e.Fun) + "(...)"
	case *ast.TypeExpr:
		return ast.SourceType(e.Type)
	case *ast.CompositeLit:
		if e.Type == nil {
			return "{...}"
		}

		return ast.SourceType(e.Type) + "{...}"
	}

	return "..."
}

// optText writes an optional bound of a slice expression.
func optText(e ast.Expr) string {
	if e == nil {
		return ""
	}

	return exprText(e)
}
