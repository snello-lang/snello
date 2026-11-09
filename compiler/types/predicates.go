// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "slices"

// Identical reports whether x and y are the same type. Named types are
// identical only when they are the same declaration; every other kind
// is identical when its parts are, as in go/types.
func Identical(x, y Type) bool {
	if x == y {
		return true
	}

	switch x := x.(type) {
	case *Basic:
		y, ok := y.(*Basic)
		return ok && x.kind == y.kind
	case *Pointer:
		y, ok := y.(*Pointer)
		return ok && Identical(x.elem, y.elem)
	case *Ref:
		y, ok := y.(*Ref)
		return ok && x.mut == y.mut && Identical(x.elem, y.elem)
	case *Option:
		y, ok := y.(*Option)
		return ok && Identical(x.elem, y.elem)
	case *Result:
		y, ok := y.(*Result)
		return ok && Identical(x.val, y.val) && Identical(x.err, y.err)
	case *Slice:
		y, ok := y.(*Slice)
		return ok && Identical(x.elem, y.elem)
	case *Array:
		y, ok := y.(*Array)
		return ok && x.len == y.len && Identical(x.elem, y.elem)
	case *Map:
		y, ok := y.(*Map)
		return ok && Identical(x.key, y.key) && Identical(x.value, y.value)
	case *Chan:
		y, ok := y.(*Chan)
		return ok && x.dir == y.dir && Identical(x.elem, y.elem)
	case *Tuple:
		y, ok := y.(*Tuple)
		return ok && identicalLists(x.elems, y.elems)
	case *Signature:
		y, ok := y.(*Signature)
		return ok && identicalVars(x.params, y.params) && Identical(x.results, y.results)
	case *Struct:
		y, ok := y.(*Struct)
		return ok && identicalFields(x.fields, y.fields)
	case *Enum:
		y, ok := y.(*Enum)
		return ok && identicalVariants(x.variants, y.variants)
	case *Named:
		return false // not the same pointer, checked above
	}

	return false
}

// AssignableTo reports whether a value of type v can be assigned to a
// place of type t without a conversion:
//
//   - the types are identical;
//   - a mut borrow goes where a ref borrow is wanted (rule 5 of
//     _docs/design/04-borrows-ref-and-mut.md);
//   - a bidirectional channel goes where a directional one is wanted;
//   - the underlying types are identical and at most one side is
//     named, so a []int literal can be assigned to `type IDs []int`.
//
// Literals take their type from context before this question is asked,
// and the implicit borrow of an owned argument passed to a ref
// parameter is a rule of calls, not of assignment.
func AssignableTo(v, t Type) bool {
	if Identical(v, t) {
		return true
	}

	if vr, ok := v.(*Ref); ok {
		tr, ok := t.(*Ref)
		return ok && !tr.mut && (vr.mut && Identical(vr.elem, tr.elem) || narrows(vr.elem, tr.elem))
	}

	if isNamed(v) && isNamed(t) {
		return false
	}

	return narrows(v, t) || Identical(v.Underlying(), t.Underlying())
}

// narrows reports whether v is a channel both ways and t the same
// channel one way, which a handle, or a borrow of one, converts to.
func narrows(v, t Type) bool {
	vc, ok := v.Underlying().(*Chan)
	if !ok || vc.dir != SendRecv {
		return false
	}

	tc, ok := t.Underlying().(*Chan)
	return ok && Identical(vc.elem, tc.elem)
}

func isChan(t Type) bool {
	_, ok := t.Underlying().(*Chan)
	return ok
}

// IsCopy reports whether assigning a value of type t copies it rather
// than moving it. The property is computed from structure, never
// declared (_docs/design/01-language-design.md): numbers, bool and
// ref borrows are Copy; strings, error, owned pointers, slices, maps,
// channel handles and mut borrows are not; a struct, enum,
// array, tuple, option or result is Copy when every part is.
//
// A function value is Copy. A closure that captured an owned value is
// the one exception, which the move checker tracks by the closure's
// captures rather than by its type.
func IsCopy(t Type) bool {
	switch t := t.(type) {
	case *Basic:
		return t.kind != String && t.kind != Error && t.kind != Invalid && t.kind != Any && t.kind != Never
	case *Ref:
		return !t.mut
	case *Signature:
		return true
	case *Option:
		return IsCopy(t.elem)
	case *Result:
		return IsCopy(t.val) && IsCopy(t.err)
	case *Array:
		return IsCopy(t.elem)
	case *Tuple:
		return allCopy(t.elems)
	case *Struct:
		return allCopyVars(t.fields)
	case *Enum:
		for _, v := range t.variants {
			if !allCopyVars(v.fields) {
				return false
			}
		}

		return true
	case *Named:
		return t.isCopy()
	case *TypeParam:
		return t.HasBound(CopyTrait)
	}

	return false // pointers, slices, maps
}

// isCopy caches the answer on the named type. A type with a Drop
// method is never Copy: a copy would be dropped twice. A declaration
// that reaches itself without a pointer in between has no finite size
// and is rejected elsewhere; here it counts as Copy so the recursion
// ends.
func (n *Named) isCopy() bool {
	if n.copyState == copyUnknown && n.Underlying() != nil {
		n.copyState = copyYes // provisional, so that a cycle back to n ends here
		if n.Implements(DropTrait) || !IsCopy(n.underlying) {
			n.copyState = copyNo
		}
	}

	return n.copyState == copyYes
}

// hasZero reports whether type t has a zero value, one that is a real
// value of it: "" for a string, 0 for a number, None for an option, an
// empty slice or map, an Err of the error's zero for a result, the
// first variant of an enum with its fields zero. A channel handle, an
// owned pointer, a borrow, a function value and a trait value have
// none, since their zero would be a null that every use followed; a
// type that implements Drop has none, since it owns something its zero
// would not; a type parameter has none, since generic code is checked
// once for every type argument; and a composite with such a part has
// none either. A declaration, a literal, make, a map read and take may
// not make the zero of a type without one.
func hasZero(t Type) bool {
	return zeroIn(t, nil)
}

// zeroIn is hasZero with the declared types it is inside, so that a
// type that contains itself, which is reported as invalid elsewhere,
// ends the walk: one met again counts as having a zero value.
func zeroIn(t Type, inside map[*Named]bool) bool {
	switch t := t.(type) {
	case *Basic:
		return t.kind != Any && t.kind != Never
	case *Option, *Slice, *Map:
		return true
	case *Result:
		return zeroIn(t.err, inside)
	case *Array:
		return zeroIn(t.elem, inside)
	case *Tuple:
		for _, e := range t.elems {
			if !zeroIn(e, inside) {
				return false
			}
		}

		return true
	case *Struct:
		return allHaveZero(t.fields, inside)
	case *Enum:
		return len(t.variants) > 0 && allHaveZero(t.variants[0].fields, inside)
	case *Named:
		if inside[t] {
			return true
		}

		if inside == nil {
			inside = map[*Named]bool{}
		}

		inside[t] = true
		defer delete(inside, t)
		return !implements(t, DropTrait) && zeroIn(t.Underlying(), inside)
	}

	return false // channels, pointers, borrows, functions, traits, type parameters
}

func allHaveZero(vs []*Var, inside map[*Named]bool) bool {
	for _, v := range vs {
		if !zeroIn(v.typ, inside) {
			return false
		}
	}

	return true
}

// isNamed reports whether t is a declared or basic type, the types that
// do not assign to each other across a name, as in Go.
func isNamed(t Type) bool {
	switch t.(type) {
	case *Basic, *Named, *TypeParam:
		return true
	}

	return false
}

func identicalLists(xs, ys []Type) bool {
	if len(xs) != len(ys) {
		return false
	}

	for i := range xs {
		if !Identical(xs[i], ys[i]) {
			return false
		}
	}

	return true
}

// identicalVars compares parameter types; names do not matter.
// identicalTypes reports whether two type lists are identical pairwise.
func identicalTypes(xs, ys []Type) bool {
	if len(xs) != len(ys) {
		return false
	}

	for i := range xs {
		if !Identical(xs[i], ys[i]) {
			return false
		}
	}

	return true
}

func identicalVars(xs, ys []*Var) bool {
	if len(xs) != len(ys) {
		return false
	}

	for i := range xs {
		if !Identical(xs[i].typ, ys[i].typ) {
			return false
		}
	}

	return true
}

// identicalFields compares fields by name and type, in order.
func identicalFields(xs, ys []*Var) bool {
	if len(xs) != len(ys) {
		return false
	}

	for i := range xs {
		if xs[i].name != ys[i].name || !Identical(xs[i].typ, ys[i].typ) {
			return false
		}
	}

	return true
}

func identicalVariants(xs, ys []*Variant) bool {
	if len(xs) != len(ys) {
		return false
	}

	for i := range xs {
		if xs[i].name != ys[i].name || !identicalVars(xs[i].fields, ys[i].fields) {
			return false
		}
	}

	return true
}

func allCopy(ts []Type) bool {
	for _, t := range ts {
		if !IsCopy(t) {
			return false
		}
	}

	return true
}

func allCopyVars(vs []*Var) bool {
	for _, v := range vs {
		if !IsCopy(v.typ) {
			return false
		}
	}

	return true
}

// dropReads reports whether dropping a value of type t may run a Drop
// method that reads what the value borrows: t implements Drop, or holds
// by value a part that does. Such a drop is a use of what the value
// borrows. What a slice, map or pointer holds cannot hold a borrow, so
// a Drop behind one reads none, and only the parts held by value count.
func dropReads(t Type) bool {
	return dropReadsIn(t, map[*Named]bool{})
}

func dropReadsIn(t Type, inside map[*Named]bool) bool {
	switch t := t.(type) {
	case *Named:
		if inside[t] {
			return false // a type that contains itself is refused elsewhere
		}

		inside[t] = true
		return implements(t, DropTrait) || t.Underlying() != nil && dropReadsIn(t.Underlying(), inside)
	case *Struct:
		return slices.ContainsFunc(t.fields, func(f *Var) bool { return dropReadsIn(f.typ, inside) })
	case *Enum:
		return slices.ContainsFunc(t.variants, func(v *Variant) bool {
			return slices.ContainsFunc(v.fields, func(f *Var) bool { return dropReadsIn(f.typ, inside) })
		})
	case *Tuple:
		return slices.ContainsFunc(t.elems, func(e Type) bool { return dropReadsIn(e, inside) })
	case *Array:
		return dropReadsIn(t.elem, inside)
	case *Option:
		return dropReadsIn(t.elem, inside)
	case *Result:
		return dropReadsIn(t.val, inside) || dropReadsIn(t.err, inside)
	}

	return false
}
