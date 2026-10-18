// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"github.com/teak-lang/teak/ast"
	"github.com/teak-lang/teak/token"
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
		c.errorf(pos, "cannot use "+describe(op)+" as "+t.String()+" in "+what)
		return false
	}

	return true
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

	if r, ok := t.(*Ref); ok {
		if !r.mut && AssignableTo(v, r.elem) {
			return true // the implicit borrow
		}

		if p, ok := v.(*Pointer); ok && !r.mut && Identical(p.elem, r.elem) {
			return true // the implicit borrow of what an owned pointer holds
		}

		if vr, ok := v.(*Ref); ok && (vr.mut || !r.mut) {
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

func fieldOf(st *Struct, name string) *Var {
	for _, f := range st.fields {
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
// option, enum, pointer and tuple of comparable parts; not slices,
// maps, functions or structs, which compare through a method.
func comparable(t Type) bool {
	switch u := t.Underlying().(type) {
	case *Basic:
		return u.kind != Any && u.kind != Never
	case *Option:
		return comparable(u.elem)
	case *Enum:
		for _, v := range u.variants {
			for _, f := range v.fields {
				if !comparable(f.typ) {
					return false
				}
			}
		}

		return true
	case *Pointer:
		return comparable(u.elem)
	case *Tuple:
		for _, e := range u.elems {
			if !comparable(e) {
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
