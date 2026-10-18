// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

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
		return ok && vr.mut && !tr.mut && Identical(vr.elem, tr.elem)
	}

	if isNamed(v) && isNamed(t) {
		return false
	}

	if vc, ok := v.Underlying().(*Chan); ok && vc.dir == SendRecv {
		if tc, ok := t.Underlying().(*Chan); ok && Identical(vc.elem, tc.elem) {
			return true
		}
	}

	return Identical(v.Underlying(), t.Underlying())
}

// IsCopy reports whether assigning a value of type t copies it rather
// than moving it. The property is computed from structure, never
// declared (_docs/design/01-language-design.md): numbers, bool,
// channel handles and ref borrows are Copy; strings, error, owned
// pointers, slices, maps and mut borrows are not; a struct, enum,
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
	case *Chan, *Signature:
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
	}

	return false // pointers, slices, maps
}

// isCopy caches the answer on the named type. A declaration that
// reaches itself without a pointer in between has no finite size and is
// rejected elsewhere; here it counts as Copy so the recursion ends.
func (n *Named) isCopy() bool {
	if n.copyState == copyUnknown && n.underlying != nil {
		n.copyState = copyYes // provisional, so that a cycle back to n ends here
		if !IsCopy(n.underlying) {
			n.copyState = copyNo
		}
	}

	return n.copyState == copyYes
}

// isNamed reports whether t is a declared or basic type, the types that
// do not assign to each other across a name, as in Go.
func isNamed(t Type) bool {
	switch t.(type) {
	case *Basic, *Named:
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
