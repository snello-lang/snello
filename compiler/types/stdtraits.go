// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/compiler/token"

// The binary operators a declared type can define, each through a
// standard trait with one method: a + b is a.Add(b). The method takes
// both operands by value, so a non-Copy operand moves into it, and
// returns Self. The right operand's type is Self unless the trait is
// listed with an argument, Mul<f64>. Unary minus is Neg, with no other
// operand. The comparisons are not operators here: == is Eq and the
// orderings go through Cmp.
var operators = [...]struct {
	op    token.Kind
	trait *Trait
	name  string
}{
	{token.ADD, AddTrait, "Add"},
	{token.SUB, SubTrait, "Sub"},
	{token.MUL, MulTrait, "Mul"},
	{token.QUO, DivTrait, "Div"},
	{token.REM, RemTrait, "Rem"},
}

// The standard traits have two kinds of implementors. The predeclared
// types implement them by structure: every number is Copy, Eq, Ord,
// Hash and Clone, a string all but Copy, and a composite of the
// predeclared kinds, a tuple, an array, an option, a slice, is what its
// parts are; a type declared over a predeclared one, type ID int, is
// what it is. A struct or enum implements one by listing it after impl,
// as it does any trait, and then either writes the trait's method or
// has it derived from its fields: Eq, Cmp, Hash and Clone can be written
// field by field whenever every field implements the trait, so a type
// that lists the trait and declares no method gets that one. Copy has
// no method and is the same check; a type with a Drop method is never
// Copy. An enum of comparable payloads is Eq and Hash by structure, as
// it has always compared with ==; a struct is only by listing them, as
// == on a struct has always gone through a method. A map key must have
// both.

// pendingKey is a map key question asked before the declarations were
// resolved, answered by resolvePending.
type pendingKey struct {
	key Type
	pos token.Pos
}

// operatorOf returns the trait and method an operator calls on a value
// of a declared type: Add and "Add" for a binary +, Neg and "Neg" for
// a unary -; nil for an operator no trait defines.
func operatorOf(op token.Kind, unary bool) (*Trait, string) {
	if unary {
		if op == token.SUB {
			return NegTrait, "Neg"
		}

		return nil, ""
	}

	for _, o := range operators {
		if o.op == op {
			return o.trait, o.name
		}
	}

	return nil, ""
}

// binaryOperator reports whether tr is one of the binary operator
// traits, which take the other operand's type as an argument.
func binaryOperator(tr *Trait) bool {
	for _, o := range operators {
		if o.trait == tr {
			return true
		}
	}

	return false
}

// origin returns the bare operator trait an instance was made from, or
// t itself.
func (t *Trait) origin() *Trait {
	if t.orig != nil {
		return t.orig
	}

	return t
}

// operatorInstance returns the operator trait t with rhs as the type of the
// other operand, Mul<f64>: a trait of its own with the one method over
// rhs, made once per argument type in this check so that identity
// stays a pointer comparison. The receiver variable is the bare trait's
// own, shared on purpose: nothing changes a Var once made.
func (c *Checker) operatorInstance(t *Trait, rhs Type) *Trait {
	for _, inst := range c.instances {
		if inst.orig == t && Identical(inst.rhs, rhs) {
			return inst
		}
	}

	want := t.required[0]
	sig := want.typ.(*Signature)
	inst := &Trait{obj: t.obj, self: t.self, standard: true, orig: t, rhs: rhs}
	inst.required = []*Func{NewFunc(token.NoPos, want.name, NewSignature(sig.recv, []*Var{NewVar(token.NoPos, "o", rhs)}, sig.results))}
	c.instances = append(c.instances, inst)
	return inst
}

// rhsOf returns the type of the other operand of the operator trait tr
// for the implementing type self: the argument written, or self when
// the trait is listed bare, since Mul is Mul<Self>.
func rhsOf(tr *Trait, self Type) Type {
	if tr.rhs != nil {
		return tr.rhs
	}

	return self
}

// listsOperator reports whether the declared type n lists the operator
// trait tr with the same other operand: Mul<V> listed on V is Mul, and
// Mul listed on V is Mul<V>.
func listsOperator(n *Named, tr *Trait) bool {
	if tr.standard && binaryOperator(tr.origin()) {
		for _, listed := range n.Origin().impls {
			if listed != nil && listed.origin() == tr.origin() && Identical(rhsOf(listed, n), rhsOf(tr, n)) {
				return true
			}
		}
	}

	return false
}

// OperatorMethod returns the method an operator calls on a value of
// type t: the operator trait's method when a declared type lists the
// trait, with any other operand, or a type parameter is bounded by it;
// nil otherwise, and for the numbers and strings, whose operators are
// built in. The backend writes the call against it.
func OperatorMethod(t Type, op token.Kind, unary bool) *Func {
	switch t := t.(type) {
	case *Named:
		if tr, name := operatorOf(op, unary); tr != nil {
			for _, listed := range t.Origin().impls {
				if listed != nil && listed.origin() == tr {
					return t.Method(name)
				}
			}
		}
	case *TypeParam:
		if tr, name := operatorOf(op, unary); tr != nil {
			for _, b := range t.bounds {
				if b != nil && b.origin() == tr {
					return t.boundMethod(name)
				}
			}
		}
	}

	return nil
}

// standardImplements answers whether t implements the standard trait
// by structure, and whether that is the whole answer: a declared type
// listing the trait is decided by its impl list, like any trait.
func standardImplements(t Type, trait *Trait) (ok, decided bool) {
	switch trait.origin() {
	case CopyTrait:
		return IsCopy(t), true
	case SendTrait:
		return IsSend(t), true
	case EqTrait:
		return comparable(t), true
	case HashTrait:
		return hashable(t), true
	case OrdTrait:
		return ordered(t), true
	case CloneTrait:
		return cloneable(t), true
	case ErrorTrait:
		if b, ok := t.(*Basic); ok {
			return b.kind == Error, true
		}
	case AddTrait, SubTrait, MulTrait, DivTrait, RemTrait, NegTrait:
		if b, ok := t.(*Basic); ok {
			return basicOperator(b, trait), true
		}
	}

	return false, false // Drop, and a declared type for Error or an operator: by the impl list
}

// basicOperator reports whether the number or string b has the operator
// trait: a number has every operator with its own type on the right, a
// string has +.
func basicOperator(b *Basic, trait *Trait) bool {
	if trait.rhs != nil && !Identical(trait.rhs, b) {
		return false
	}

	return b.info&IsNumeric != 0 || b.kind == String && trait.origin() == AddTrait
}

// IsStringer reports whether fmt prints a value of type t with its
// String method, as Go's fmt prints a fmt.Stringer: a declared type
// that lists fmt's Stringer trait after impl, or a trait refining it, a
// type parameter bounded by one, or a trait value of one.
func IsStringer(t Type) bool {
	switch t := t.(type) {
	case *Named:
		for _, tr := range t.Origin().impls {
			if tr != nil && refinesStringer(tr) {
				return true
			}
		}
	case *TypeParam:
		for _, b := range t.bounds {
			if b != nil && refinesStringer(b) {
				return true
			}
		}
	case *Trait:
		return refinesStringer(t)
	}

	return false
}

// refinesStringer reports whether t is fmt's Stringer or refines it.
// Stringer is declared by the library, so it is known by its name and
// package, not as a predeclared trait.
func refinesStringer(t *Trait) bool {
	if t.obj != nil && t.obj.name == "Stringer" && t.obj.pkg != nil && t.obj.pkg.path == "fmt" {
		return true
	}

	for _, r := range t.refines {
		if r != nil && refinesStringer(r) {
			return true
		}
	}

	return false
}

// isErrorType reports whether a value of type t prints as its message:
// the predeclared error, a declared type implementing Error, a type
// parameter bounded by it, or a trait value of a trait refining it.
func isErrorType(t Type) bool {
	switch t := t.(type) {
	case *Basic:
		return t.kind == Error
	case *Named:
		return t.Implements(ErrorTrait)
	case *TypeParam:
		return t.HasBound(ErrorTrait)
	case *Trait:
		return t.Refines(ErrorTrait)
	}

	return false
}

// ordered reports whether a value of type t has Cmp: numbers and
// strings do, a declared type that lists Ord, and a type declared over
// an ordered one.
func ordered(t Type) bool {
	switch u := t.(type) {
	case *Basic:
		return u.info&(IsNumeric|IsString) != 0
	case *Named:
		return u.Implements(OrdTrait) || !composite(u) && ordered(u.Underlying())
	}

	return false
}

// cloneable reports whether a value of type t has Clone: every
// predeclared type and composite of them does, with the owned memory
// copied, except a borrow, which has nothing of its own to copy; a
// channel handle clones to another handle on the same channel, and a
// function value copies, as it is Copy. A declared type does when it
// lists Clone, or is declared over a type that clones.
func cloneable(t Type) bool {
	switch u := t.(type) {
	case *Basic:
		return u.kind != Invalid && u.kind != Any && u.kind != Never
	case *Chan, *Signature:
		return true
	case *Pointer:
		return cloneable(u.elem)
	case *Slice:
		return cloneable(u.elem)
	case *Array:
		return cloneable(u.elem)
	case *Option:
		return cloneable(u.elem)
	case *Result:
		return cloneable(u.val) && cloneable(u.err)
	case *Map:
		return cloneable(u.key) && cloneable(u.value)
	case *Tuple:
		for _, e := range u.elems {
			if !cloneable(e) {
				return false
			}
		}

		return true
	case *Named:
		return u.Implements(CloneTrait) || !composite(u) && cloneable(u.Underlying())
	}

	return false
}

// composite reports whether the declared type is a struct or an enum,
// the declarations that can list traits; any other declared type is
// what its underlying type is.
func composite(n *Named) bool {
	switch n.Underlying().(type) {
	case *Struct, *Enum:
		return true
	}

	return false
}

// derivable reports whether a type listing the standard trait without
// its method gets the method from its fields. Each of these traits
// requires exactly one method, which deriveMethod relies on.
func derivable(tr *Trait) bool {
	switch tr {
	case CopyTrait, EqTrait, OrdTrait, HashTrait, CloneTrait:
		return true
	}

	return false
}

// deriveListed gives the declared type n the methods of the standard
// trait listed, and of the traits it refines, that n did not declare,
// written from its fields; the first refused derivation is reported,
// once, since the refined traits' derivations fail on the same field.
func (c *Checker) deriveListed(n *Named, listed *Trait, pos token.Pos) {
	refused := ""
	for _, tr := range listed.lineage() {
		msg := deriveMethod(n, listed, tr)
		if refused == "" {
			refused = msg
		}
	}

	if refused != "" {
		c.errorf(pos, refused)
	}

	if listed.origin() == HashTrait {
		eq, hash := n.Method("Eq"), n.Method("Hash")
		if eq != nil && hash != nil && eq.derived != hash.derived { // equal values must hash alike, which the compiler promises only for the pair it wrote
			c.errorf(pos, n.obj.name+" writes one of Eq and Hash and derives the other: write both or neither, since equal values must hash alike")
		}
	}
}

// deriveMethod gives the declared type n the method of the standard
// trait tr it listed, or reached through listed, without declaring the
// method: written from the fields, when every field implements the
// trait. It returns the message refusing the derivation, at the first
// field that does not implement the trait, or "". The method is added
// either way, so that a refused derivation is reported once, not again
// as a missing method. A type that wrote the method keeps it, checked
// as any method is.
func deriveMethod(n *Named, listed, tr *Trait) string {
	if !derivable(tr) {
		return ""
	}

	if tr != CopyTrait {
		want := tr.required[0] // the one method a derivable trait requires
		if n.Method(want.name) != nil {
			return ""
		}

		fn := substFunc(want, map[*TypeParam]Type{tr.self: n})
		fn.derived = true
		n.AddMethod(fn)
	}

	reason := undeserved(n, tr)
	if tr == CopyTrait && n.Implements(DropTrait) {
		reason = "it implements Drop"
	}

	if reason == "" {
		return ""
	}

	if tr != listed {
		reason += ", required by " + listed.obj.name
	}

	return n.obj.name + " does not implement " + tr.obj.name + ": " + reason
}

// undeserved returns why the fields of n do not give it the standard
// trait tr: the first field whose type does not implement it, or "".
func undeserved(n *Named, tr *Trait) string {
	switch u := n.Underlying().(type) {
	case *Struct:
		for _, f := range u.fields {
			if !implements(f.typ, tr) {
				return "field " + f.name + " has type " + f.typ.String() + ", which is not " + tr.obj.name
			}
		}
	case *Enum:
		for _, v := range u.variants {
			for _, f := range v.fields {
				if !implements(f.typ, tr) {
					return "variant " + v.name + " has a field of type " + f.typ.String() + ", which is not " + tr.obj.name
				}
			}
		}
	}

	return ""
}

// keyable reports whether a value of type key may be a map key: it
// must implement Hash, and so Eq, which every comparable predeclared
// type does by structure; a trait value never is one, whatever its
// trait refines, since the map could not call its Hash. Before the
// declarations are resolved the question waits, since an enum's
// payloads are not known yet.
func (c *Checker) keyable(key Type, pos token.Pos) bool {
	if !c.declared {
		c.pendingKeys = append(c.pendingKeys, pendingKey{key, pos})
		return true
	}

	if _, isTrait := key.(*Trait); isTrait {
		c.errorf(pos, key.String()+" cannot be a map key: a trait value cannot be one")
		return false
	}

	if implements(key, HashTrait) {
		return true
	}

	c.errorf(pos, key.String()+" cannot be a map key: it does not implement Hash")
	return false
}
