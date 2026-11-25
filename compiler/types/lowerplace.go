// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/token"
)

// The places of the form: an assignment's place reached where the
// store is made, the path of a place through its fields, elements and
// borrows, and the types the lowering asks about.

// place evaluates the place e an assignment writes, reaching it where
// the store is made: its indexes were evaluated before the values when
// indexed says so, and a call it is reached through runs once.
func (l *lowering) place(e ast.Expr, indexed bool) Operand {
	if p, ok := l.placeOf(e); ok {
		if !indexed {
			l.indexes(e)
		}

		return p
	}

	switch x := e.(type) {
	case *ast.ParenExpr:
		return l.place(x.X, indexed)
	case *ast.SelectorExpr:
		return l.baseValue(x.X).sub(x.Sel.Name)
	case *ast.UnaryExpr:
		if x.Op == token.MUL {
			return l.baseValue(x.X).sub("*")
		}
	case *ast.IndexExpr:
		base := l.baseValue(x.X) // what is indexed before the index, as the program evaluates them
		if !indexed {
			l.value(x.Index, nil)
		}

		return base.sub("[]")
	}

	return l.baseValue(e)
}

// indexes evaluates the indexes and keys inside the place e, the inner
// first, as the program evaluates them before an assignment's values;
// the variables the place is reached through are not read, only
// written to.
func (l *lowering) indexes(e ast.Expr) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		l.indexes(x.X)
	case *ast.SelectorExpr:
		l.indexes(x.X)
	case *ast.UnaryExpr:
		l.indexes(x.X)
	case *ast.IndexExpr:
		l.indexes(x.X)
		l.value(x.Index, nil)
	}
}

// baseValue evaluates a value a place is reached through, a call's
// result, once within the statement.
func (l *lowering) baseValue(e ast.Expr) Operand {
	for _, r := range l.bases {
		if r.e == e {
			return r.o
		}
	}

	o := l.value(e, nil)
	l.bases = append(l.bases, reached{e, o})
	return o
}

// placeOf returns the operand of a place expression: a variable, with
// the path of fields, derefs and elements reached from it; false for a
// value made here.
func (l *lowering) placeOf(e ast.Expr) (Operand, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if v, ok := l.c.info.Uses[x].(*Var); ok && !v.field {
			return Operand{Var: l.index(v)}, true
		}

		if x.Name == "super" && l.c.recv != nil { // the receiver of the method being lowered, through any closures
			return Operand{Var: l.index(l.c.recv)}, true
		}
	case *ast.ParenExpr:
		return l.placeOf(x.X)
	case *ast.SelectorExpr:
		if l.isPkgSelector(x) {
			if v, ok := l.c.info.Uses[x.Sel].(*Var); ok && v.global {
				return Operand{Var: l.index(v)}, true
			}

			return Operand{}, false
		}

		if v, isField := l.c.info.Uses[x.Sel].(*Var); !isField || !v.field {
			return Operand{}, false // a method, not a part of the place
		}

		if p, ok := l.placeOf(x.X); ok {
			return p.sub(l.throughBorrow(x.X, x.Sel.Name)), true
		}
	case *ast.UnaryExpr:
		if x.Op == token.MUL {
			if p, ok := l.placeOf(x.X); ok {
				return p.sub("*"), true
			}
		}
	case *ast.IndexExpr:
		if p, ok := l.placeOf(x.X); ok {
			return p.sub(l.throughBorrow(x.X, "[]")), true
		}
	case *ast.SliceExpr:
		if p, ok := l.placeOf(x.X); ok {
			return p.sub(l.throughBorrow(x.X, "[]")), true
		}
	case *ast.PartExpr:
		if p, ok := l.placeOf(x.X); ok {
			return p.sub(x.Part.Name), true
		}
	}

	return Operand{}, false
}

// throughBorrow is the path part of a field or element reached from
// base, through the deref a borrow or a pointer goes through when base
// is one.
func (l *lowering) throughBorrow(base ast.Expr, part string) string {
	switch l.typeOf(base).(type) {
	case *Ref, *Pointer:
		return "*." + part
	}

	return part
}

// isPkgSelector reports whether a selector names a package's member.
func (l *lowering) isPkgSelector(sel *ast.SelectorExpr) bool {
	_, isPkg := l.c.info.Uses[selectorBase(sel)].(*PkgName)
	return isPkg
}

// kindOf says how the place p is reached from its variable: owned,
// through a borrow or through an element; a temporary is owned.
func (l *lowering) kindOf(p Operand) placeKind {
	if p.IsTemp() {
		return placeOwned
	}

	kind, _ := placeKindOf(l.f.Vars[p.Var].typ, p.Path)
	return kind
}

// Types.

func (l *lowering) typeOf(e ast.Expr) Type {
	if l.c.info == nil {
		return nil
	}

	return l.c.info.Types[e]
}

// channel evaluates the channel an operation is on, read and never
// moved: every operation on a channel goes through a borrow of it.
func (l *lowering) channel(e ast.Expr) Operand {
	if p, ok := l.placeOf(e); ok {
		l.emit(Instr{Op: OpRead, Place: p, Expr: e, Pos: e.Pos()})
		return p
	}

	return l.value(e, nil)
}

// sendType is the element type of the channel a send writes to, nil
// for a send the checker refused.
func (l *lowering) sendType(ch ast.Expr) Type {
	if c := chanOf(l.typeOf(ch)); c != nil {
		return c.elem
	}

	return nil
}

func selectorBase(sel *ast.SelectorExpr) *ast.Ident {
	id, _ := unparen(sel.X).(*ast.Ident)
	return id
}
