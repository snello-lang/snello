// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/compiler/ast"
	"github.com/teak-lang/teak/compiler/token"
)

// A mode says what kind of thing an expression denotes, beside its
// type, as go/types.operandMode does. The ownership passes read it: a
// place can be borrowed or moved out of, a value only moved.
type mode int8

const (
	invalid     mode = iota // an error was reported; nothing more is said about it
	value                   // a computed value, owned by the expression
	variable                // an owned place: a variable, or a field or element of one
	borrowed                // a place reached through a ref borrow
	mutBorrowed             // a place reached through a mut borrow
	typeOperand             // a type in expression position: a conversion or make
)

// An operand is what checking an expression yields: a mode, a type, and
// the object behind it when the expression names one, which a call
// needs to tell a built-in, a variant constructor and a function apart.
type operand struct {
	mode mode
	typ  Type
	obj  Object
	expr ast.Expr // the expression, for the move pass; nil for a value made up here
}

var invalidOperand = operand{invalid, Typ[Invalid], nil, nil}

// deref strips the borrows and owned pointers in front of a type, for
// a field access, method call or index through them, and says what
// kind of place the result is: through a ref it is borrowed, through a
// mut it is mutably borrowed, through an owned pointer it is the
// owner's place.
func deref(x operand) (Type, mode) {
	t, m := x.typ, x.mode
	for {
		switch u := t.(type) {
		case *Ref:
			t = u.elem
			m = borrowed
			if u.mut {
				m = mutBorrowed
			}
		case *Pointer:
			t = u.elem
			if m == value {
				m = variable
			}
		default:
			return t, m
		}
	}
}

// condition checks the condition of an if or a for.
func (c *Checker) condition(e ast.Expr) {
	c.assign(c.expr(e, Typ[Bool]), Typ[Bool], e.Pos(), "condition")
}

// assign reports an error when op cannot be used where a value of type
// t is wanted; what says the role, for the message. A place that fits
// is moved there, unless t lends it.
func (c *Checker) assign(op operand, t Type, pos token.Pos, what string) {
	if c.fits(op, t, pos, what) {
		c.maybeMove(op, t)
	}
}

// fits reports whether op can be used where t is wanted, reporting the
// mismatch, without moving anything: for an operator that reads its
// operand.
func (c *Checker) fits(op operand, t Type, pos token.Pos, what string) bool {
	if op.mode == invalid || t == nil {
		return false
	}

	if !assignable(op, t) {
		c.errorf(pos, "cannot use "+describe(op)+" as "+t.String()+" in "+what+traitReason(op.typ, t))
		return false
	}

	return true
}

// traitReason says why a value cannot become a trait value, after the
// mismatch message, when t is a trait; empty otherwise.
func traitReason(v, t Type) string {
	tr, ok := t.(*Trait)
	if !ok || v == Typ[Invalid] {
		return ""
	}

	switch v := v.(type) {
	case *Ref:
		return ": a trait value is made from an owned value, not a borrow"
	case *Pointer:
		return ": a pointer does not become a trait value; the value it holds does"
	case *Trait:
		return ": a trait value cannot be used as another trait yet"
	case *TypeParam:
		return ": a type parameter cannot become a trait value yet"
	case *Named:
		if !v.Implements(tr) {
			return ": " + v.obj.name + " does not implement " + tr.obj.name
		}

		return ": " + v.obj.name + " holds a borrow and cannot become a trait value"
	}

	return ": only a declared type implements a trait"
}

// convertsToTrait reports whether an owned value of type v becomes a
// value of the trait type t: a declared type implementing t does, and
// the value is moved into the trait value, or copied when it is Copy.
// A type holding a borrow does not: the trait value hides what it
// holds, so the borrow could no longer be tied to its owner. A borrow
// does not, since a trait value owns what it holds, and another
// trait's value does not yet.
func convertsToTrait(v Type, t *Trait) bool {
	n, ok := v.(*Named)
	return ok && n.Implements(t) && !holdsBorrow(n)
}

// assignable reports whether op can be used where t is wanted. Beyond
// AssignableTo it knows the rules of borrows at use sites: an owned
// value or place passed where ref T is wanted is borrowed for the call
// (05-standard-library.md, section 5); a borrow of an owned pointer
// reaches the pointed-to value (ref *T is usable as ref T); a borrow of
// a Copy value is read as the value; a diverging expression fits any
// type; and an invalid type fits everything, so an error is reported
// once.
func assignable(op operand, t Type) bool {
	v := op.typ
	if v == Typ[Invalid] || t == Typ[Invalid] || isNever(v) || isAny(t) {
		return true
	}

	if AssignableTo(v, t) {
		return true
	}

	if tr, ok := t.(*Trait); ok {
		return convertsToTrait(v, tr)
	}

	if r, ok := t.(*Ref); ok {
		if !r.mut && AssignableTo(v, r.elem) {
			return true // the implicit borrow
		}

		if p, ok := v.(*Pointer); ok && !r.mut && Identical(p.elem, r.elem) {
			return true // the implicit borrow of what an owned pointer holds
		}

		if vr, ok := v.(*Ref); ok && (vr.mut || !r.mut) {
			if Identical(vr.elem, r.elem) {
				return true // a mut passed on as a ref
			}

			if p, ok := vr.elem.(*Pointer); ok && Identical(p.elem, r.elem) {
				return true // ref *T as ref T
			}
		}

		return false
	}

	if vr, ok := v.(*Ref); ok && IsCopy(vr.elem) {
		return AssignableTo(vr.elem, t) // a Copy value read through a borrow
	}

	return false
}

// valueType is the type an operand has when it is read as a value: a
// borrow of a Copy type reads as the type itself, as 5 + n does through
// a ref int.
func valueType(op operand) Type {
	if r, ok := op.typ.(*Ref); ok && IsCopy(r.elem) {
		return r.elem
	}

	return op.typ
}

// hintOf is the type an operand offers as a hint to the other side of
// an operator or assignment: its value type, or the invalid type after
// an error, which silences what depends on it.
func hintOf(op operand) Type {
	if op.mode == invalid {
		return Typ[Invalid]
	}

	return valueType(op)
}

// readType is the type an operator reads from an operand: a borrow of
// any type reads as the type, since an operator only looks. `+` on two
// ref strings builds a new string from both.
func readType(op operand) Type {
	if r, ok := op.typ.(*Ref); ok {
		return r.elem
	}

	return op.typ
}

// describe names an operand for a message: its type, and for a named
// thing its name.
func describe(op operand) string {
	if op.obj != nil && op.obj.Name() != "" {
		return op.obj.Name() + " (" + op.typ.String() + ")"
	}

	return "a value of type " + op.typ.String()
}

// fieldNamed returns the field with the given name among fields, or nil.
func fieldNamed(fields []*Var, name string) *Var {
	for _, f := range fields {
		if f.name == name {
			return f
		}
	}

	return nil
}

func underlyingPointer(t Type) (*Pointer, bool) {
	if t == nil {
		return nil, false
	}

	p, ok := t.Underlying().(*Pointer)
	return p, ok
}

func hasInfo(t Type, info BasicInfo) bool {
	b, ok := t.Underlying().(*Basic)
	return ok && b.info&info != 0
}

// comparable reports whether == is defined on t: every basic type,
// option, enum, pointer and tuple of comparable parts, a declared type
// listing Eq and a type parameter bounded by it; not slices, maps or
// functions, nor a struct that does not list Eq.
func comparable(t Type) bool { return comparableBy(t, EqTrait) }

// hashable reports whether a value of type t can be a map key: the
// comparable types, with Hash in place of Eq for a declared type's
// list and a type parameter's bounds.
func hashable(t Type) bool { return comparableBy(t, HashTrait) }

// comparableBy walks t as comparable does, asking a declared type and a
// type parameter for trait, Eq or Hash, and every part of a composite
// the same question.
func comparableBy(t Type, trait *Trait) bool {
	if n, ok := t.(*Named); ok && n.Implements(trait) {
		return true // a struct compares by listing Eq, with the method written or derived
	}

	switch u := t.Underlying().(type) {
	case *TypeParam:
		return u.HasBound(trait)
	case *Basic:
		return u.kind != Any && u.kind != Never
	case *Option:
		return comparableBy(u.elem, trait)
	case *Enum:
		for _, v := range u.variants {
			for _, f := range v.fields {
				if !comparableBy(f.typ, trait) {
					return false
				}
			}
		}

		return true
	case *Pointer:
		return comparableBy(u.elem, trait)
	case *Tuple:
		for _, e := range u.elems {
			if !comparableBy(e, trait) {
				return false
			}
		}

		return true
	case *Chan:
		return true
	}

	return false
}

func isNever(t Type) bool {
	b, ok := t.(*Basic)
	return ok && b.kind == Never
}

func isAny(t Type) bool {
	b, ok := t.(*Basic)
	return ok && b.kind == Any
}

func isString(t Type) bool {
	b, ok := t.Underlying().(*Basic)
	return ok && b.kind == String
}

func isMap(t Type) bool {
	_, ok := t.Underlying().(*Map)
	return ok
}

func isByteSlice(t Type) bool {
	s, ok := t.Underlying().(*Slice)
	if !ok {
		return false
	}

	b, ok := s.elem.(*Basic)
	return ok && b.kind == Uint8
}

func isLiteral(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return isLiteral(e.X)
	case *ast.UnaryExpr:
		return e.Op == token.SUB && isLiteral(e.X)
	}

	return false
}
